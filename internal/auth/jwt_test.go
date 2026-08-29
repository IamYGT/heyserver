package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"
	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateAndValidateToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		user *models.User
		role models.Role
	}{
		{"admin", testutil.MakeUser(1, "a@x.com", models.RoleAdmin), models.RoleAdmin},
		{"manager", testutil.MakeUser(2, "m@x.com", models.RoleManager), models.RoleManager},
		{"viewer", testutil.MakeUser(3, "v@x.com", models.RoleViewer), models.RoleViewer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok, err := auth.GenerateToken(testutil.TestSecret, tc.user)
			if err != nil { t.Fatalf("GenerateToken: %v", err) }
			claims, err := auth.ValidateToken(testutil.TestSecret, tok)
			if err != nil { t.Fatalf("ValidateToken: %v", err) }
			if claims.UserID != tc.user.ID { t.Errorf("UserID mismatch") }
			if claims.Role != tc.role { t.Errorf("Role mismatch") }
			if claims.Issuer != "hserver-panel" { t.Errorf("Issuer mismatch") }
		})
	}
}

func TestValidateToken_Expired(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	claims := auth.Claims{
		UserID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			Issuer:    "hserver-panel",
		},
	}
	signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testutil.TestSecret))
	_, err := auth.ValidateToken(testutil.TestSecret, signed)
	if err == nil { t.Error("should reject expired token") }
}

func TestValidateToken_WrongSigningMethod(t *testing.T) {
	t.Parallel()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	user := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	claims := auth.Claims{
		UserID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1*time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "hserver-panel",
		},
	}
	signed, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaKey)
	_, err := auth.ValidateToken(testutil.TestSecret, signed)
	if err == nil { t.Error("should reject RS256 token") }
}

func TestValidateToken_WrongSecret(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	tok, _ := auth.GenerateToken("correct-secret-32-bytes-minimum!", user)
	_, err := auth.ValidateToken("wrong-secret-32-bytes-for-verify!", tok)
	if err == nil { t.Error("should fail with wrong secret") }
}

func TestValidateToken_Malformed(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, token string }{
		{"empty", ""},
		{"garbage", "not.a.jwt"},
		{"two parts", "header.payload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.ValidateToken(testutil.TestSecret, tc.token)
			if err == nil { t.Errorf("ValidateToken(%q) should error", tc.token) }
		})
	}
}

func TestPasswordHashing(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, password string }{
		{"simple", "password123"},
		{"complex", "P@ssw0rd!"},
		{"unicode", "парол€"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hash, err := auth.HashPassword(tc.password)
			if err != nil { t.Fatalf("HashPassword: %v", err) }
			if hash == tc.password { t.Error("hash equals plaintext") }
			if !auth.CheckPassword(hash, tc.password) { t.Error("CheckPassword false for correct") }
		})
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	t.Parallel()
	hash, _ := auth.HashPassword("correct")
	tests := []struct{ name, pw string }{
		{"wrong", "wrong"}, {"empty", ""}, {"case", "Correct"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if auth.CheckPassword(hash, tc.pw) { t.Errorf("CheckPassword(%q) should be false", tc.pw) }
		})
	}
}

func TestGenerateToken_UniquePerUser(t *testing.T) {
	t.Parallel()
	u1 := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	u2 := testutil.MakeUser(2, "b@x.com", models.RoleViewer)
	t1, _ := auth.GenerateToken(testutil.TestSecret, u1)
	t2, _ := auth.GenerateToken(testutil.TestSecret, u2)
	if t1 == t2 { t.Error("tokens must differ per user") }
}
