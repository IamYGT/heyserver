package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/security"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestHandleLogin_BadJSON(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{not-json"))
	rec := httptest.NewRecorder()

	handleLogin(testCfg(), security.NewRateLimiter())(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["error"] != "invalid request body" {
		t.Errorf("error = %q, want %q", body["error"], "invalid request body")
	}
}

func TestHandleLogin_EmptyEmail(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"","password":"secret"}`))
	rec := httptest.NewRecorder()

	handleLogin(testCfg(), security.NewRateLimiter())(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["error"] != "email and password are required" {
		t.Errorf("error = %q, want %q", body["error"], "email and password are required")
	}
}

func TestHandleLoginRejectsAmbiguousAndOversizedJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "unknown field", body: `{"email":"admin@example.test","password":"secret","remember":true}`, want: http.StatusBadRequest},
		{name: "trailing JSON", body: `{"email":"admin@example.test","password":"secret"}{}`, want: http.StatusBadRequest},
		{name: "oversized", body: `{"email":"` + strings.Repeat("x", authRequestBodyLimit) + `","password":"secret"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handleLogin(testCfg(), security.NewRateLimiter())(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(test.body)))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandleLoginRecordsFailuresInInjectedLimiter(t *testing.T) {
	repo := db.NewUserRepository(db.Instance())
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	user := &models.User{Email: "rate-limit-auth@example.test", Name: "Rate Limit", Password: hash, Role: models.RoleAdmin}
	if err := repo.Create(user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Delete(user.ID) })

	limiter := security.NewRateLimiter()
	handler := handleLogin(testCfg(), limiter)
	const ip = "198.51.100.87"
	for attempt := 0; attempt < 10; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"rate-limit-auth@example.test","password":"wrong-password"}`))
		req.RemoteAddr = ip + ":1234"
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, body = %s", attempt+1, recorder.Code, recorder.Body.String())
		}
	}
	if banned, _ := limiter.IsBanned(ip); !banned {
		t.Fatal("injected login limiter did not observe handler failures")
	}
}

func TestHandleMe_NoToken(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()

	handleMe()(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["error"] != "unauthorized" {
		t.Errorf("error = %q, want %q", body["error"], "unauthorized")
	}
}
