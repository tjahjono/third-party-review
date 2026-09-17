package repository

import (
	"context"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// SettingsRepository manages the single app_settings row. There is exactly
// one: the table's boolean primary key forces that, so Get never needs a key
// and Update never needs an INSERT ... ON CONFLICT dance.
type SettingsRepository interface {
	GetRiskMatrix(ctx context.Context) (*model.RiskMatrix, error)
	UpdateRiskMatrix(ctx context.Context, m *model.RiskMatrix) error
}

type settingsRepository struct {
	db helper.ConnProvider
}

func NewSettingsRepository(db helper.ConnProvider) SettingsRepository {
	return &settingsRepository{db: db}
}

func (r *settingsRepository) GetRiskMatrix(ctx context.Context) (*model.RiskMatrix, error) {
	const q = `
		SELECT risk_medium_min, risk_high_min, risk_critical_min, updated_at, updated_by
		  FROM app_settings WHERE id`
	var m model.RiskMatrix
	err := r.db.Querier(ctx).QueryRow(ctx, q).
		Scan(&m.MediumMin, &m.HighMin, &m.CriticalMin, &m.UpdatedAt, &m.UpdatedBy)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &m, nil
}

func (r *settingsRepository) UpdateRiskMatrix(ctx context.Context, m *model.RiskMatrix) error {
	const q = `
		UPDATE app_settings
		   SET risk_medium_min = $1, risk_high_min = $2, risk_critical_min = $3,
		       updated_at = now(), updated_by = $4
		 WHERE id
		RETURNING updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, m.MediumMin, m.HighMin, m.CriticalMin, m.UpdatedBy).
		Scan(&m.UpdatedAt)
	return helper.MapErr(err)
}
