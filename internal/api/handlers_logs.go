package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/services/logs"
)

// ── GET /api/logs/sources ─────────────────────────────────────────────────────

// handleLogSources lists all discoverable log files with metadata.
//
// Response: { "sources": [ LogSource, ... ] }
func handleLogSources(logSvc *logs.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sources, err := logSvc.ListSources()
		if err != nil {
			slog.Error("failed to list log sources", "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to list log sources")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"sources": sources})
	}
}

// ── GET /api/logs/read?path=X&lines=100 ───────────────────────────────────────

// handleLogRead returns the last N lines of a log file.
//
// Query params:
//   - path  — absolute path to the log file (required)
//   - lines — number of lines to return (default 100, max 5000)
//
// Response: { "path": "...", "lines": [ "..." ], "total": N }
func handleLogRead(logSvc *logs.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		n := 100
		if raw := r.URL.Query().Get("lines"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				jsonError(w, http.StatusBadRequest, "lines must be a positive integer")
				return
			}
			n = parsed
		}

		result, err := logSvc.TailLines(path, n)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// ── GET /api/logs/search?path=X&query=Y&limit=500 ────────────────────────────

// handleLogSearch performs a case-insensitive substring search inside a log file.
//
// Query params:
//   - path  — absolute path to the log file (required)
//   - query — search term (required)
//   - limit — max results to return (default 500, max 2000)
//
// Response: { "path": "...", "lines": [ "..." ], "total": N }
func handleLogSearch(logSvc *logs.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		query := r.URL.Query().Get("query")

		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}
		if query == "" {
			jsonError(w, http.StatusBadRequest, "query is required")
			return
		}

		limit := 500
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				jsonError(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			limit = parsed
		}

		result, err := logSvc.SearchLog(path, query, limit)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// ── GET /api/logs/stream?path=X ───────────────────────────────────────────────

// handleLogStream streams new log lines via Server-Sent Events (SSE).
//
// The client receives:
//
//	data: <log line>\n\n          — for each new line
//	: ping\n\n                    — heartbeat every 15 s (keep proxies alive)
//
// The stream ends when the client disconnects.
func handleLogStream(logSvc *logs.Service, shutdownCtx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamCtx, cancel := requestStreamContext(r.Context(), shutdownCtx)
		defer cancel()
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		// Verify path access before setting SSE headers.
		if !logSvc.IsAllowed(path) {
			jsonError(w, http.StatusForbidden, "access denied: path not in allowed list")
			return
		}

		// Verify the file exists and is readable.
		if _, err := os.Stat(filepath.Clean(path)); err != nil {
			jsonError(w, http.StatusNotFound, "log file not found")
			return
		}

		// SSE headers — must be set before any write.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering
		w.WriteHeader(http.StatusOK)

		// Disable the server's WriteTimeout for this connection.
		// SSE streams are long-lived; the global WriteTimeout (30 s) would kill them.
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			// Non-fatal: server may not support deadline override; proceed anyway.
			_ = err
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			// Should never happen with standard http.ResponseWriter.
			_, _ = fmt.Fprint(w, "data: streaming not supported\n\n")
			return
		}
		// Send a protocol comment immediately so proxies release the response
		// headers and the UI can report a connected stream without waiting for
		// the first log line or heartbeat.
		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ch := make(chan string, 64)
		if err := logSvc.StreamNewLines(streamCtx, path, ch); err != nil {
			slog.Error("log stream error", "err", err)
			_, _ = fmt.Fprint(w, "data: error: stream unavailable\n\n")
			flusher.Flush()
			return
		}

		for {
			select {
			case <-streamCtx.Done():
				return
			case line, open := <-ch:
				if !open {
					return
				}
				if line == ": ping" {
					// SSE comment — keeps connection alive.
					_, _ = fmt.Fprint(w, ": ping\n\n")
				} else {
					// SSE injection guard: newlines inside a log line would
					// break the SSE framing and allow protocol-level injection.
					// Replace them with a visible placeholder before writing.
					safe := strings.NewReplacer("\r\n", "↵", "\r", "↵", "\n", "↵").Replace(line)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", safe)
				}
				flusher.Flush()
			}
		}
	}
}

// ── GET /api/logs/download?path=X ────────────────────────────────────────────

// handleLogDownload sends the log file as an attachment download.
// Files larger than 50 MB are rejected.
func handleLogDownload(logSvc *logs.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		if err := logSvc.CheckDownloadable(path); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			jsonError(w, http.StatusNotFound, "log file not found")
			return
		}
		defer func() { _ = f.Close() }()

		info, _ := f.Stat()
		filename := filepath.Base(path)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		if info != nil {
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		}

		if _, err := io.Copy(w, f); err != nil {
			// Headers already sent — nothing we can do but log.
			return
		}
	}
}
