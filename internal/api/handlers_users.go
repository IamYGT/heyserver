package api

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
)

// reEmail is a simple RFC-5322-subset regex for validating email addresses at
// the handler boundary.  It intentionally rejects edge cases (quoted strings,
// comments) that are valid in RFC-5322 but never used in practice.
var reEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func isValidEmail(s string) bool { return len(s) <= 254 && reEmail.MatchString(s) }

type userCreateRequest struct {
	Email    string      `json:"email"`
	Name     string      `json:"name"`
	Password string      `json:"password"`
	Role     models.Role `json:"role"`
}

type userUpdateRequest struct {
	Email    *string      `json:"email"`
	Name     *string      `json:"name"`
	Role     *models.Role `json:"role"`
	Password *string      `json:"password"`
}

// handleUserList returns a paginated list of all users.
func handleUserList() http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := parsePagination(r)

		list, err := users.List(limit, offset)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not list users")
			return
		}

		total, err := users.Count()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not count users")
			return
		}

		// Scrub password hashes before sending.
		for i := range list {
			list[i].Password = ""
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"data":   list,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// handleUserCreate creates a new user. The requesting user must be an admin.
func handleUserCreate(cfg *config.Config) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		var req userCreateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Email == "" || req.Name == "" || req.Password == "" {
			jsonError(w, http.StatusBadRequest, "email, name and password are required")
			return
		}
		if !isValidEmail(req.Email) {
			jsonError(w, http.StatusBadRequest, "invalid email format")
			return
		}
		if len(req.Name) > 100 {
			jsonError(w, http.StatusBadRequest, "name must be 100 characters or fewer")
			return
		}
		if req.Role == "" {
			req.Role = models.RoleViewer
		}
		if !isValidRole(req.Role) {
			jsonError(w, http.StatusBadRequest, "invalid role: must be admin, manager or viewer")
			return
		}
		if len(req.Password) < 8 {
			jsonError(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		if len(req.Password) > 128 {
			jsonError(w, http.StatusBadRequest, "password must be 128 characters or fewer")
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not hash password")
			return
		}

		user := &models.User{
			Email:    req.Email,
			Name:     req.Name,
			Password: hash,
			Role:     req.Role,
		}

		if err := users.Create(user); err != nil {
			if err == db.ErrDuplicateEmail {
				jsonError(w, http.StatusConflict, "email already in use")
				return
			}
			jsonError(w, http.StatusInternalServerError, "could not create user")
			return
		}

		actor := getUserFromContext(r.Context())
		_ = audit.Insert(buildAuditEntry(
			actor.ID, actor.Name,
			"user_created", "users",
			"created user "+user.Email,
			r,
		))

		user.Password = ""
		jsonResponse(w, http.StatusCreated, user)
	}
}

// handleUserUpdate updates a user's name, email and/or role.
// Optionally updates the password if "password" key is present and non-empty.
func handleUserUpdate() http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseIDParam(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid user id")
			return
		}

		existing, err := users.FindByID(id)
		if err == db.ErrNotFound {
			jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not fetch user")
			return
		}

		var req userUpdateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Email == nil && req.Name == nil && req.Role == nil && req.Password == nil {
			jsonError(w, http.StatusBadRequest, "at least one user field is required")
			return
		}

		if req.Email != nil {
			if !isValidEmail(*req.Email) {
				jsonError(w, http.StatusBadRequest, "invalid email format")
				return
			}
			existing.Email = *req.Email
		}
		if req.Name != nil {
			if *req.Name == "" || len(*req.Name) > 100 {
				jsonError(w, http.StatusBadRequest, "name must be between 1 and 100 characters")
				return
			}
			existing.Name = *req.Name
		}
		if req.Role != nil {
			if !isValidRole(*req.Role) {
				jsonError(w, http.StatusBadRequest, "invalid role")
				return
			}
			existing.Role = *req.Role
		}
		if req.Password != nil {
			if len(*req.Password) < 8 {
				jsonError(w, http.StatusBadRequest, "password must be at least 8 characters")
				return
			}
			if len(*req.Password) > 128 {
				jsonError(w, http.StatusBadRequest, "password must be 128 characters or fewer")
				return
			}
		}

		if err := users.UpdateWithPassword(existing, req.Password); err != nil {
			if err == db.ErrNotFound {
				jsonError(w, http.StatusNotFound, "user not found")
				return
			}
			if err == db.ErrDuplicateEmail {
				jsonError(w, http.StatusConflict, "email already in use")
				return
			}
			if err == db.ErrLastAdmin {
				jsonError(w, http.StatusConflict, "at least one admin account is required")
				return
			}
			jsonError(w, http.StatusInternalServerError, "could not update user")
			return
		}

		actor := getUserFromContext(r.Context())
		_ = audit.Insert(buildAuditEntry(
			actor.ID, actor.Name,
			"user_updated", "users",
			"updated user "+existing.Email,
			r,
		))

		existing.Password = ""
		jsonResponse(w, http.StatusOK, existing)
	}
}

// handleUserDelete deletes a user. Admins cannot delete themselves.
func handleUserDelete() http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseIDParam(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid user id")
			return
		}

		actor := getUserFromContext(r.Context())
		if actor.ID == id {
			jsonError(w, http.StatusBadRequest, "cannot delete your own account")
			return
		}

		target, err := users.FindByID(id)
		if err == db.ErrNotFound {
			jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not fetch user")
			return
		}

		if err := users.Delete(id); err != nil {
			if err == db.ErrNotFound {
				jsonError(w, http.StatusNotFound, "user not found")
				return
			}
			if err == db.ErrLastAdmin {
				jsonError(w, http.StatusConflict, "at least one admin account is required")
				return
			}
			jsonError(w, http.StatusInternalServerError, "could not delete user")
			return
		}

		_ = audit.Insert(buildAuditEntry(
			actor.ID, actor.Name,
			"user_deleted", "users",
			"deleted user "+target.Email,
			r,
		))

		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- helpers ---------------------------------------------------------------

func isValidRole(r models.Role) bool {
	return r == models.RoleAdmin || r == models.RoleManager || r == models.RoleViewer
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 20
	offset = 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200 // hard cap — prevents accidental full-table scans
			}
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

func parseIDParam(r *http.Request, name string) (int64, error) {
	val := r.PathValue(name)
	return strconv.ParseInt(val, 10, 64)
}
