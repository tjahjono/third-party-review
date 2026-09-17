package settings

import (
	"context"
	"log/slog"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/repository"

	"github.com/google/uuid"
)

type Service struct {
	settings repository.SettingsRepository
	log      *slog.Logger
}

func New(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &Service{settings: deps.Settings, log: deps.Log}, nil
}

// MustNew is New for wiring that cannot meaningfully recover.
func MustNew(deps Deps) *Service {
	s, err := New(deps)
	if err != nil {
		panic(err)
	}
	return s
}

// GetRiskMatrix reads the persisted matrix.
func (s *Service) GetRiskMatrix(ctx context.Context) (*model.RiskMatrix, error) {
	return s.settings.GetRiskMatrix(ctx)
}

// LoadRiskMatrix reads the persisted matrix and installs it as the one
// helper.Band and helper.BandFromFloat compute against for the rest of the
// process. Called once at startup, before the server accepts requests, so
// every page renders against the setting that's actually on record rather
// than the compiled-in default.
func (s *Service) LoadRiskMatrix(ctx context.Context) error {
	m, err := s.settings.GetRiskMatrix(ctx)
	if err != nil {
		return err
	}
	helper.SetRiskMatrix(*m)
	return nil
}

// UpdateRiskMatrix validates and persists a new matrix, then immediately
// installs it process-wide - every already-open page picks up the new
// mapping on its next render, with no restart needed.
func (s *Service) UpdateRiskMatrix(ctx context.Context, m *model.RiskMatrix, userID *uuid.UUID) error {
	if !helper.ValidRiskMatrix(*m) {
		return helper.ValidationError{
			Field: "risk_matrix",
			Message: "Each threshold must be between 1 and 5, and Medium ≤ High ≤ Critical " +
				"(two equal thresholds are fine - that band is just skipped).",
		}
	}
	m.UpdatedBy = userID
	if err := s.settings.UpdateRiskMatrix(ctx, m); err != nil {
		return err
	}
	helper.SetRiskMatrix(*m)
	s.log.Info("risk matrix updated",
		"medium_min", m.MediumMin, "high_min", m.HighMin, "critical_min", m.CriticalMin)
	return nil
}
