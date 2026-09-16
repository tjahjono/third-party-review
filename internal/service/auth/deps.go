package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"third-party-review/internal/repository"
)

type Deps struct {
	Users    repository.UserRepository
	Sessions repository.SessionRepository
	// RecoveryCodes is the MFA fallback set. It is its own repository because
	// it is its own table: spending a code is a delete, not an update to the
	// user row.
	RecoveryCodes repository.RecoveryCodeRepository

	// SessionTTL is how long a fully authenticated session lasts. A session
	// still pending its second factor gets a much shorter life, fixed in this
	// package, because it is a credential that has passed only one check.
	SessionTTL time.Duration

	Log *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("auth: missing dependency")

func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Users == nil:
		missing = "Users"
	case d.Sessions == nil:
		missing = "Sessions"
	case d.RecoveryCodes == nil:
		missing = "RecoveryCodes"
	case d.Log == nil:
		missing = "Log"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
}

// FromRepositories builds Deps from the full repository set held by the main
// package.
func FromRepositories(repos *repository.Repositories, sessionTTL time.Duration, log *slog.Logger) Deps {
	if repos == nil {
		return Deps{SessionTTL: sessionTTL, Log: log}
	}
	return Deps{
		Users:         repos.Users,
		Sessions:      repos.Sessions,
		RecoveryCodes: repos.RecoveryCodes,
		SessionTTL:    sessionTTL,
		Log:           log,
	}
}
