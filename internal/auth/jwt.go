package auth

import (
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type Claims struct {
	UserID int64       `json:"userId"`
	Email  string      `json:"email"`
	Name   string      `json:"name"`
	Role   models.Role `json:"role"`
	jwt.RegisteredClaims
}

// TokenTTL is the lifetime of an access token. 24 hours is the
// maximum recommended for a server management panel.
const TokenTTL = 24 * time.Hour

func GenerateToken(secret string, user *models.User) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		Name:   user.Name,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			// NotBefore prevents token use before issuance (clock skew guard).
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			Issuer:    "hserver-panel",
			Audience:  jwt.ClaimStrings{"hserver-panel"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ValidateToken(secret string, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		},
		// Enforce issuer and audience to prevent token reuse across services.
		jwt.WithIssuer("hserver-panel"),
		jwt.WithAudience("hserver-panel"),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return claims, nil
}

// BcryptCost is the work factor used when hashing passwords.
// OWASP recommends a minimum of 12; bcrypt.DefaultCost is 10.
const BcryptCost = 12

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(bytes), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummySentinel is a bcrypt hash of a fixed string used exclusively by
// DummyCheckPassword. It is computed once at startup with the same cost as
// real password hashes so that the dummy comparison takes the same wall-clock
// time as a real failed login, preventing timing-based account enumeration.
var dummySentinel, _ = bcrypt.GenerateFromPassword([]byte("__hserver_dummy_sentinel__"), BcryptCost)

// DummyCheckPassword runs a bcrypt comparison against a pre-computed sentinel
// hash. Call this whenever a user lookup fails so that the response time is
// indistinguishable from a real wrong-password attempt.
func DummyCheckPassword() {
	// Intentionally discard the result — the comparison will always fail.
	_ = bcrypt.CompareHashAndPassword(dummySentinel, []byte("__dummy__"))
}
