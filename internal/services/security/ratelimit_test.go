package security_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/security"
)

func newTestRateLimiter(t *testing.T) *security.RateLimiter {
	t.Helper()
	return security.NewRateLimiter()
}

func TestRateLimiter_AllowWithinBurst(t *testing.T) {
	t.Parallel()

	rl := newTestRateLimiter(t)
	ip := "203.0.113.10"

	// LimitGeneral burst is 30 — first burst requests should all succeed immediately.
	for i := 0; i < 30; i++ {
		if !rl.Allow(ip, security.LimitGeneral) {
			t.Fatalf("Allow burst request %d: got false want true", i+1)
		}
	}
}

func TestRateLimiter_RecordFailureTriggersBan(t *testing.T) {
	t.Parallel()

	rl := newTestRateLimiter(t)
	ip := "203.0.113.20"

	for i := 0; i < 9; i++ {
		if banned := rl.RecordFailure(ip); banned {
			t.Fatalf("RecordFailure %d: unexpected ban", i+1)
		}
	}

	if banned, _ := rl.IsBanned(ip); banned {
		t.Fatal("should not be banned before max failures")
	}

	if !rl.RecordFailure(ip) {
		t.Fatal("RecordFailure at max: expected ban=true")
	}

	banned, remaining := rl.IsBanned(ip)
	if !banned {
		t.Fatal("IsBanned: expected true after max failures")
	}
	if remaining <= 0 {
		t.Errorf("ban remaining: got %v want > 0", remaining)
	}
}

func TestRateLimiter_MiddlewareReturns429WhenBanned(t *testing.T) {
	t.Parallel()

	rl := newTestRateLimiter(t)
	ip := "203.0.113.30"

	for i := 0; i < 10; i++ {
		rl.RecordFailure(ip)
	}

	handler := rl.Middleware(security.LimitLogin)(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status: got %d want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("X-Ban-Remaining"); got == "" {
		t.Error("expected X-Ban-Remaining header when banned")
	}
	if body := rec.Body.String(); !strings.Contains(body, "too many requests") {
		t.Errorf("body %q should mention too many requests", body)
	}
}

func TestRealIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  func() *http.Request
		want string
	}{
		{
			name: "X-Forwarded-For first hop",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Forwarded-For", "203.0.113.40, 10.0.0.1")
				r.RemoteAddr = "192.168.1.1:12345"
				return r
			},
			want: "203.0.113.40",
		},
		{
			name: "X-Forwarded-For trimmed",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Forwarded-For", "  198.51.100.5  ")
				return r
			},
			want: "198.51.100.5",
		},
		{
			name: "X-Real-IP fallback",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Real-IP", "198.51.100.6")
				r.RemoteAddr = "10.0.0.2:8080"
				return r
			},
			want: "198.51.100.6",
		},
		{
			name: "RemoteAddr fallback",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.RemoteAddr = "203.0.113.50:443"
				return r
			},
			want: "203.0.113.50",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := security.RealIP(tc.req())
			if got != tc.want {
				t.Errorf("RealIP: got %q want %q", got, tc.want)
			}
		})
	}
}
