package helper

import (
	"errors"
	"fmt"
)

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

// ErrNoQuestions is returned when a file parses cleanly but yields no usable
// question rows.
var ErrNoQuestions = errors.New("no question rows found")

// ValidationError describes a single field-level validation failure. It is
// rendered directly into HTMX partials, so Message is user-facing prose.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
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

// ParseError is a user-facing ingestion failure. Message is written for a
// security assessor, not a developer, because it is rendered straight into an
// HTMX partial.
type ParseError struct {
	Message string `json:"message"`
	// Hint suggests the next action, e.g. which column to map manually.
	Hint string `json:"hint,omitempty"`
	Err  error  `json:"-"`
}

func (e *ParseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *ParseError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrInvalidInput) succeed for any ParseError, so
// handlers map them to a 422 partial rather than a 500.
func (e *ParseError) Is(target error) bool { return target == ErrInvalidInput }

// NewParseError builds a user-facing ingestion failure.
func NewParseError(message, hint string, err error) *ParseError {
	return &ParseError{Message: message, Hint: hint, Err: err}
}
