// Package settings manages app-wide configuration that has exactly one value
// for the whole (single-tenant) deployment - currently just the severity risk
// matrix.
package settings

import (
	"errors"
	"fmt"
	"log/slog"

	"third-party-review/internal/repository"
)

type Deps struct {
	Settings repository.SettingsRepository
	Log      *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("settings: missing dependency")

func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Settings == nil:
		missing = "Settings"
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
		Settings: repos.Settings,
		Log:      log,
	}
}
