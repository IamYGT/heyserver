package api

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/models"
)

// panelOrigins contains only local development origins. Production serves the
// SPA and API from the same origin, so it does not need a provider-specific
// CORS exception.
var panelOrigins = []string{
	"http://localhost:3085",
	"http://localhost:5173", // Vite dev server
}

type contextKey string

const (
	userContextKey contextKey = "user"
)

type RoleLevel int

const (
	RoleViewer  RoleLevel = 1
	RoleManager RoleLevel = 2
	RoleAdmin   RoleLevel = 3
)

func roleLevel(r models.Role) RoleLevel {
	switch r {
	case models.RoleAdmin:
		return RoleAdmin
	case models.RoleManager:
		return RoleManager
	default:
		return RoleViewer
	}
}

func withAuth(cfg *config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := ""

		// Check Authorization header first
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// Fallback to cookie
		if tokenStr == "" {
			if cookie, err := r.Cookie("hserver_token"); err == nil {
				tokenStr = cookie.Value
			}
		}

		// Browsers cannot attach an Authorization header to a WebSocket upgrade.
		// Accept the existing panel JWT as a secondary WebSocket subprotocol so
		// sessions that predate the HttpOnly cookie migration can still open the
		// writable terminal without putting the token in the URL.
		if tokenStr == "" && r.URL.Path == "/api/terminal/ws" {
			for _, protocol := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
				protocol = strings.TrimSpace(protocol)
				if strings.HasPrefix(protocol, "jwt.") {
					tokenStr = strings.TrimPrefix(protocol, "jwt.")
					break
				}
			}
		}

		// Legacy fallback for non-browser WebSocket clients. The panel itself uses
		// the secure same-origin session cookie and never places JWTs in URLs.
		if tokenStr == "" {
			if q := r.URL.Query().Get("token"); q != "" {
				tokenStr = q
			}
		}

		if tokenStr == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		claims, err := auth.ValidateToken(cfg.JWTSecret, tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		user := &models.User{
			ID:    claims.UserID,
			Email: claims.Email,
			Name:  claims.Name,
			Role:  claims.Role,
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

func requireRole(minRole RoleLevel, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getUserFromContext(r.Context())
		if user == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		if roleLevel(user.Role) < minRole {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

func getUserFromContext(ctx context.Context) *models.User {
	user, _ := ctx.Value(userContextKey).(*models.User)
	return user
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(wrapped, r)

		// Log only the path, never the raw query string.
		// Legacy clients may still supply ?token=. Never expose it in access logs.
		logPath := r.URL.Path
		if q := r.URL.Query(); q.Get("token") != "" {
			// Indicate that a token was present without revealing its value.
			logPath += "?token=[REDACTED]"
		}

		slog.Info("request",
			"method", r.Method,
			"path", logPath,
			"status", wrapped.status,
			"duration", time.Since(start).String(),
			"ip", r.RemoteAddr,
		)
	})
}

var boundedLongOperationPrefixes = []string{
	"/api/nodes/",            // bounded remote SSH operations
	"/api/php/composer/",     // composerTimeout: 5 minutes
	"/api/backups/gdrive/",   // OAuth/rclone calls: up to 2 minutes synchronously
	"/api/backups/snapshot/", // restic status/list/purge: up to 10 minutes
	"/api/ssl/",              // certbot issue/renew: up to 5 minutes
	"/api/disk/",             // dirsize/cleanup workers: up to 60 seconds
	"/api/system/update/",    // release archive staging: up to 15 minutes
}

func isBoundedLongOperationPath(path string) bool {
	if path == "/api/domains" || strings.HasPrefix(path, "/api/domains/") {
		return true // provisioning can run certbot for up to 3 minutes
	}
	if path == "/api/databases/pgm-restore" {
		return true // pg_restore/mysql restore is bounded to 5 minutes
	}
	for _, prefix := range boundedLongOperationPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// withBoundedOperationWriteDeadline lets explicitly bounded host operations
// use their own service-level timeout instead of being cut off by the HTTP
// server's shorter global write deadline.
func withBoundedOperationWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBoundedLongOperationPath(r.URL.Path) {
			allowLongSystemAction(w)
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedOrigin returns true when the given origin matches one of the
// pre-defined panel origins.
func isAllowedOrigin(origin string) bool {
	for _, allowed := range panelOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// isHTTPS reports whether the original request arrived over HTTPS.
// Because the server runs behind nginx (which terminates TLS), r.TLS is
// always nil. We rely on the X-Forwarded-Proto header set by nginx instead.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// withCORS sets hardened HTTP response headers on every response and handles
// CORS for allowed origins. It short-circuits OPTIONS preflight with 204.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// --- Security headers (applied to every response) ---

		// Remove the Server header to avoid technology fingerprinting.
		// Go's net/http does not set it by default, but be explicit.
		h.Del("Server")

		// Prevent MIME-type sniffing attacks.
		h.Set("X-Content-Type-Options", "nosniff")

		// Disallow framing entirely — the panel is never embedded in an iframe.
		h.Set("X-Frame-Options", "DENY")

		// Modern guidance: disable the legacy XSS auditor; rely on CSP instead.
		h.Set("X-XSS-Protection", "0")

		// Control the Referer header sent with outbound requests.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy — panel is a self-contained SPA.
		// connect-src includes wss: for the WebSocket terminal.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self' wss:; "+
				"font-src 'self' data:; "+
				"frame-ancestors 'none'")

		// Disable browser features the panel does not need.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		// HSTS — only meaningful when served over HTTPS; harmless otherwise.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Prevent intermediate caches from storing API responses.
		// Static asset responses can override this header themselves.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}

		// --- CORS headers ---
		// The panel SPA is served from the same origin, so CORS is only
		// needed for development environments and explicit cross-origin callers.
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin) {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Max-Age", "86400") // cache preflight for 24 h
			// Vary must list Origin so caches key responses correctly.
			h.Add("Vary", "Origin")
		}

		// Short-circuit preflight requests — no body, no further processing.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

const maxRequestBodySize = 32 << 20 // 32 MB

// withBodyLimit enforces a maximum request body size on POST/PUT/PATCH requests.
// This prevents large payload DoS attacks.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController reach the underlying connection for
// per-handler deadline overrides used by long-running maintenance and streams.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Hijack implements http.Hijacker so that gorilla/websocket can upgrade the
// connection. Without this, the websocket upgrader cannot find the Hijacker
// interface on the wrapped ResponseWriter and returns a 500 error.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Flush implements http.Flusher so SSE streams (logs, backup jobs) can push events.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
