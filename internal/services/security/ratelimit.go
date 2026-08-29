package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LimitType categorises the kind of rate limit to apply.
type LimitType string

const (
	LimitLogin   LimitType = "login"
	LimitAPI     LimitType = "api"
	LimitGeneral LimitType = "general"

	// LimitMutate applies to state-changing operations that are not login-related
	// but should be throttled to prevent abuse: user management, firewall rules,
	// SSL certificate issuance, backup/restore, deploys, IP blacklist changes.
	// Allows 1 request per 2 seconds with a burst of 5.
	LimitMutate LimitType = "mutate"

	// LimitWebhook applies to the unauthenticated deploy webhook endpoint.
	// Allows 2 requests per 5 seconds with a burst of 3 to handle rapid pushes.
	LimitWebhook LimitType = "webhook"
)

type clientState struct {
	limiter     *rate.Limiter
	lastSeen    time.Time
	failures    int
	bannedUntil time.Time
}

// RateLimiter manages per-IP token-bucket limiters and soft bans.
type RateLimiter struct {
	mu          sync.Mutex
	clients     map[string]map[LimitType]*clientState
	limits      map[LimitType]rate.Limit
	bursts      map[LimitType]int
	maxFailures int
	banDuration time.Duration
	cleanupTTL  time.Duration
}

func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]map[LimitType]*clientState),
		limits: map[LimitType]rate.Limit{
			LimitLogin:   rate.Every(12 * time.Second),
			LimitAPI:     rate.Every(600 * time.Millisecond),
			LimitGeneral: rate.Every(300 * time.Millisecond),
			LimitMutate:  rate.Every(2 * time.Second),
			LimitWebhook: rate.Every(2500 * time.Millisecond),
		},
		bursts: map[LimitType]int{
			LimitLogin:   5,
			LimitAPI:     20,
			LimitGeneral: 30,
			LimitMutate:  5,
			LimitWebhook: 3,
		},
		maxFailures: 10,
		banDuration: 15 * time.Minute,
		cleanupTTL:  5 * time.Minute,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) Allow(ip string, lt LimitType) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cs := rl.getOrCreate(ip, lt)
	if !cs.bannedUntil.IsZero() && time.Now().Before(cs.bannedUntil) {
		return false
	}
	cs.lastSeen = time.Now()
	return cs.limiter.Allow()
}

func (rl *RateLimiter) RecordFailure(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cs := rl.getOrCreate(ip, LimitLogin)
	cs.failures++
	cs.lastSeen = time.Now()
	if cs.failures >= rl.maxFailures {
		cs.bannedUntil = time.Now().Add(rl.banDuration)
		cs.failures = 0
		return true
	}
	return false
}

func (rl *RateLimiter) RecordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if types, ok := rl.clients[ip]; ok {
		if cs, ok := types[LimitLogin]; ok {
			cs.failures = 0
			cs.bannedUntil = time.Time{}
		}
	}
}

func (rl *RateLimiter) IsBanned(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if types, ok := rl.clients[ip]; ok {
		if cs, ok := types[LimitLogin]; ok {
			if !cs.bannedUntil.IsZero() && time.Now().Before(cs.bannedUntil) {
				return true, time.Until(cs.bannedUntil)
			}
		}
	}
	return false, 0
}

func (rl *RateLimiter) Middleware(lt LimitType) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ip := RealIP(r)
			if !rl.Allow(ip, lt) {
				if banned, remaining := rl.IsBanned(ip); banned {
					w.Header().Set("X-Ban-Remaining", remaining.String())
				}
				http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
				return
			}
			next(w, r)
		}
	}
}

func (rl *RateLimiter) getOrCreate(ip string, lt LimitType) *clientState {
	if _, ok := rl.clients[ip]; !ok {
		rl.clients[ip] = make(map[LimitType]*clientState)
	}
	if _, ok := rl.clients[ip][lt]; !ok {
		rl.clients[ip][lt] = &clientState{
			limiter:  rate.NewLimiter(rl.limits[lt], rl.bursts[lt]),
			lastSeen: time.Now(),
		}
	}
	return rl.clients[ip][lt]
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupTTL)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		threshold := time.Now().Add(-rl.cleanupTTL)
		for ip, types := range rl.clients {
			for lt, cs := range types {
				if cs.lastSeen.Before(threshold) {
					delete(types, lt)
				}
			}
			if len(types) == 0 {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// RealIP extracts the client IP from proxy headers or RemoteAddr.
func RealIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := xff
		for i, c := range xff {
			if c == ',' {
				first = xff[:i]
				break
			}
		}
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip.String()
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
