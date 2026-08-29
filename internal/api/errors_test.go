package api

import "testing"

func TestNotFoundError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  NotFoundError
		want string
	}{
		{"custom message", NotFoundError{Resource: "user", Message: "gone"}, "gone"},
		{"resource only", NotFoundError{Resource: "domain"}, "domain not found"},
		{"empty", NotFoundError{}, "not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  ValidationError
		want string
	}{
		{"field and message", ValidationError{Field: "email", Message: "required"}, "email: required"},
		{"message only", ValidationError{Message: "invalid payload"}, "invalid payload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestForbiddenError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  ForbiddenError
		want string
	}{
		{"custom message", ForbiddenError{Message: "admin only"}, "admin only"},
		{"default", ForbiddenError{}, "forbidden"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConflictError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  ConflictError
		want string
	}{
		{"custom message", ConflictError{Resource: "user", Message: "duplicate"}, "duplicate"},
		{"resource only", ConflictError{Resource: "domain"}, "domain already exists"},
		{"empty", ConflictError{}, "conflict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}
