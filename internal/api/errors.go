package api


// ── Sentinel error types ──────────────────────────────────────────────────────

// NotFoundError is returned when a requested resource does not exist.
type NotFoundError struct {
	Resource string
	Message  string
}

func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Resource != "" {
		return e.Resource + " not found"
	}
	return "not found"
}

// ValidationError is returned when the request payload fails validation.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return e.Field + ": " + e.Message
	}
	return e.Message
}

// ForbiddenError is returned when the caller lacks the required permission.
type ForbiddenError struct {
	Message string
}

func (e *ForbiddenError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "forbidden"
}

// ConflictError is returned when a resource already exists or state conflicts.
type ConflictError struct {
	Resource string
	Message  string
}

func (e *ConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Resource != "" {
		return e.Resource + " already exists"
	}
	return "conflict"
}

