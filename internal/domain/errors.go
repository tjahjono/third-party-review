package domain

import "errors"

// Sentinel errors shared across layers. Repositories return these instead of
// driver-specific errors so the service and delivery layers can map them to
// user-facing outcomes without importing a database package.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInvalidInput  = errors.New("invalid input")
	ErrNotPermitted  = errors.New("not permitted")
	ErrAlreadyExists = errors.New("already exists")
)

// ValidationError describes a single field-level validation failure. It is
// rendered directly into HTMX partials, so Message is user-facing prose.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// Is lets errors.Is(err, ErrInvalidInput) succeed for any ValidationError.
func (e ValidationError) Is(target error) bool { return target == ErrInvalidInput }

// ValidationErrors is a collection of field failures reported together so a
// form can highlight every problem in one round trip.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	switch len(e) {
	case 0:
		return "validation failed"
	case 1:
		return e[0].Error()
	}
	msg := e[0].Error()
	for _, v := range e[1:] {
		msg += "; " + v.Error()
	}
	return msg
}

func (e ValidationErrors) Is(target error) bool { return target == ErrInvalidInput }

// Add appends a field failure.
func (e *ValidationErrors) Add(field, message string) {
	*e = append(*e, ValidationError{Field: field, Message: message})
}

// OrNil returns nil when no failures were recorded, so callers can
// unconditionally `return errs.OrNil()`.
func (e ValidationErrors) OrNil() error {
	if len(e) == 0 {
		return nil
	}
	return e
}
