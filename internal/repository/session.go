package repository

import (
	"context"
	"time"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type SessionRepository interface {
	Create(ctx context.Context, s *model.Session) error
	GetByID(ctx context.Context, id string) (*model.Session, error)
	Promote(ctx context.Context, id string, expiresAt time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
}

type sessionRepository struct {
	db helper.ConnProvider
}

func NewSessionRepository(db helper.ConnProvider) SessionRepository {
	return &sessionRepository{db: db}
}

func (r *sessionRepository) Create(ctx context.Context, s *model.Session) error {
	const q = `
		INSERT INTO sessions (id, user_id, mfa_pending, user_agent, ip, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, s.ID, s.UserID, s.MFAPending,
		truncate(s.UserAgent, 512), s.IP, s.ExpiresAt).Scan(&s.CreatedAt)
	return helper.MapErr(err)
}

func (r *sessionRepository) GetByID(ctx context.Context, id string) (*model.Session, error) {
	const q = `SELECT id, user_id, mfa_pending, user_agent, ip, expires_at, created_at
	             FROM sessions WHERE id = $1`
	var s model.Session
	err := r.db.Querier(ctx).QueryRow(ctx, q, id).Scan(&s.ID, &s.UserID, &s.MFAPending,
		&s.UserAgent, &s.IP, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &s, nil
}

// Promote clears the MFA-pending flag after a TOTP code is verified and
// extends the session to its full lifetime.
func (r *sessionRepository) Promote(ctx context.Context, id string, expiresAt time.Time) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE sessions SET mfa_pending = FALSE, expires_at = $2 WHERE id = $1`, id, expiresAt)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

func (r *sessionRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return helper.MapErr(err)
}

func (r *sessionRepository) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, helper.MapErr(err)
	}
	return int(tag.RowsAffected()), nil
}
