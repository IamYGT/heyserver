package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRoleConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role Role
		want string
	}{
		{RoleAdmin, "admin"},
		{RoleManager, "manager"},
		{RoleViewer, "viewer"},
	}

	for _, tt := range tests {
		if string(tt.role) != tt.want {
			t.Errorf("Role constant = %q, want %q", tt.role, tt.want)
		}
	}
}

func TestUserJSONTags(t *testing.T) {
	t.Parallel()

	u := User{
		ID:          1,
		Email:       "user@example.com",
		Name:        "Test User",
		Password:    "secret",
		Role:        RoleAdmin,
		TOTPSecret:  "totp-secret",
		TOTPEnabled: true,
		CreatedAt:   time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC),
	}

	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, key := range []string{"id", "email", "name", "role", "totp_enabled", "createdAt", "updatedAt"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing expected json key %q in %s", key, string(raw))
		}
	}

	for _, key := range []string{"password", "totp_secret", "Password", "TOTPSecret"} {
		if _, ok := m[key]; ok {
			t.Errorf("sensitive field %q must not appear in JSON", key)
		}
	}

	var roundTrip User
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if roundTrip.Email != u.Email || roundTrip.Role != u.Role {
		t.Fatalf("round-trip mismatch: %+v", roundTrip)
	}
	if roundTrip.Password != "" || roundTrip.TOTPSecret != "" {
		t.Fatal("omitted fields should remain empty on unmarshal")
	}
}

func TestUserJSONTagSanity(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(User{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			t.Errorf("field %s missing json tag", field.Name)
			continue
		}
		if strings.Contains(tag, " ") {
			t.Errorf("field %s json tag contains whitespace: %q", field.Name, tag)
		}
		if strings.HasPrefix(tag, "json:") {
			t.Errorf("field %s json tag looks malformed: %q", field.Name, tag)
		}
	}
}
