package api

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
)

var validAuditNodeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// handleAuditList returns a paginated and optionally filtered list of audit logs.
//
// Query parameters:
//
//	limit    — page size (default 50, max 200)
//	offset   — skip this many rows
//	user_id  — filter by user
//	action   — filter by action string (e.g. "login", "user_created")
//	action_contains — case-insensitive action-name search
//	resource — filter by resource string (e.g. "users", "auth")
//	user     — case-insensitive display-name search
//	server   — system-operation scope: "local" or a managed-node ID
//	from     — ISO-8601 lower bound for created_at
//	to       — ISO-8601 upper bound for created_at
func handleAuditList() http.HandlerFunc {
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := parsePagination(r)
		q := r.URL.Query()

		filter := db.AuditFilter{
			Action:         q.Get("action"),
			ActionContains: q.Get("action_contains"),
			Resource:       q.Get("resource"),
			UserName:       q.Get("user"),
			Server:         q.Get("server"),
		}
		if filter.Server != "" && filter.Server != "local" && !validAuditNodeID.MatchString(filter.Server) {
			jsonError(w, http.StatusBadRequest, "server must be local or a valid managed-node ID")
			return
		}

		if v := q.Get("user_id"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				filter.UserID = id
			}
		}
		if v := q.Get("from"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				filter.From = &t
			}
		}
		if v := q.Get("to"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				filter.To = &t
			}
		}

		entries, total, err := audit.List(filter, limit, offset)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not list audit logs")
			return
		}

		// Return empty array rather than null when no entries exist.
		if entries == nil {
			entries = []models.AuditLog{}
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"data":   entries,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	}
}
