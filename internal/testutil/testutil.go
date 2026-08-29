package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/golang-jwt/jwt/v5"
)

const TestSecret = "test-secret-32-bytes-at-minimum!!"

// TestConfig returns a config suitable for handler/router integration tests.
func TestConfig() *config.Config {
	return &config.Config{
		JWTSecret:           TestSecret,
		CronSecret:          "test-cron-secret-32-bytes-min!!",
		DataDir:             "/tmp/hserver-test-data",
		VhostsRoot:          "/tmp/hserver-test-vhosts",
		PHPConfigRoot:       "/tmp/hserver-test-php/config",
		PHPBinaryRoot:       "/tmp/hserver-test-php/bin",
		NginxSitesAvailable: "/tmp/hserver-test-nginx/available",
		NginxSitesEnabled:   "/tmp/hserver-test-nginx/enabled",
		PM2AllowedRoots:     []string{"/tmp/hserver-test-vhosts"},
	}
}

func MakeUser(id int64, email string, role models.Role) *models.User {
	return &models.User{ID: id, Email: email, Name: "Test User", Role: role}
}
func MakeToken(t *testing.T, user *models.User) string {
	t.Helper()
	tok, err := auth.GenerateToken(TestSecret, user)
	if err != nil {
		t.Fatalf("testutil.MakeToken: %v", err)
	}
	return tok
}
func MakeExpiredToken(t *testing.T, user *models.User) string {
	t.Helper()
	claims := auth.Claims{
		UserID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			Issuer:    "hserver-panel",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(TestSecret))
	if err != nil {
		t.Fatalf("testutil.MakeExpiredToken: %v", err)
	}
	return signed
}
func NewRequest(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}
func NewRequestWithCookie(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: "hserver_token", Value: token})
	return req
}
func ParseJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
		t.Fatalf("testutil.ParseJSON: %v (body: %s)", err, rec.Body.String())
	}
}
