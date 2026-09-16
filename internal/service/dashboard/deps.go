// Package dashboard aggregates cross-assessment figures for the home page.
// It is read-only: every method composes existing repository queries into one
// view rather than owning any state of its own.
package dashboard

import (
	"errors"
	"fmt"
	"log/slog"

	"third-party-review/internal/repository"
)

type Deps struct {
	Vendors     repository.VendorRepository
	Assessments repository.AssessmentRepository
	Jobs        repository.ReviewJobRepository
	Log         *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("dashboard: missing dependency")

func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Vendors == nil:
		missing = "Vendors"
	case d.Assessments == nil:
		missing = "Assessments"
	case d.Jobs == nil:
		missing = "Jobs"
	case d.Log == nil:
		missing = "Log"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
}

// FromRepositories builds Deps from the full repository set held by the main
// package.
func FromRepositories(repos *repository.Repositories, log *slog.Logger) Deps {
	if repos == nil {
		return Deps{Log: log}
	}
	return Deps{
		Vendors:     repos.Vendors,
		Assessments: repos.Assessments,
		Jobs:        repos.Jobs,
		Log:         log,
	}
}
