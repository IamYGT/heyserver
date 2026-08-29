package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_UserCreateRejectsUnknownAndTrailingJSON(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	for name, body := range map[string]string{
		"unknown field": `{"name":"Community User","email":"community@example.com","password":"valid-pass-123","role":"viewer","admin":true}`,
		"trailing JSON": `{"name":"Community User","email":"community@example.com","password":"valid-pass-123","role":"viewer"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIntegration_UserUpdateRequiresAFieldAndRejectsUnknownJSON(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	for name, body := range map[string]string{
		"empty object":  `{}`,
		"unknown field": `{"enabled":false}`,
		"trailing JSON": `{"name":"Admin"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/users/1", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIntegration_UserUpdateValidatesPasswordBeforeProfileMutation(t *testing.T) {
	handler := integrationRouter(t)
	repo := db.NewUserRepository(db.Instance())
	hash, err := auth.HashPassword("existing-pass-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user := &models.User{
		Email:    fmt.Sprintf("atomic-user-%d@example.com", time.Now().UnixNano()),
		Name:     "Original Name",
		Password: hash,
		Role:     models.RoleViewer,
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(user.ID) })

	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/users/%d", user.ID), strings.NewReader(`{"name":"Changed Name","password":"short"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	stored, err := repo.FindByID(user.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Name != "Original Name" {
		t.Fatalf("profile changed before password validation: name = %q", stored.Name)
	}
}
