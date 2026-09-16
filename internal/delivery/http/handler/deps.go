package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"third-party-review/internal/service"
)

type Deps struct {
	Assessments service.AssessmentFacade
	Reviews     service.ReviewService
	Dashboards  service.DashboardService
	Auth        service.AuthService

	// Templates is a concrete renderer rather than an interface: it is a pure
	// function of the embedded template set with no I/O to fake, and an
	// interface here would buy nothing but indirection.
	Templates *Renderer

	// SessionTTL is the cookie lifetime issued on a successful login. It has
	// to match what the auth service stamps on the session row, so it is
	// injected rather than read independently.
	SessionTTL time.Duration

	Log *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("handler: missing dependency")

func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Assessments == nil:
		missing = "Assessments"
	case d.Reviews == nil:
		missing = "Reviews"
	case d.Dashboards == nil:
		missing = "Dashboards"
	case d.Auth == nil:
		missing = "Auth"
	case d.Templates == nil:
		missing = "Templates"
	case d.Log == nil:
		missing = "Log"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
}
