package postgres

import (
	"context"
	"time"

	"third-party-review/internal/domain"
)

// SessionRepo is the PostgreSQL implementation of domain.SessionRepository.
// Sessions live in the database rather than in memory so a restart does not
// log the team out mid-review.
type SessionRepo struct{ db *DB }

func (r *SessionRepo) Create(ctx context.Context, s *domain.Session) error {
	const q = `
		INSERT INTO sessions (id, user_id, mfa_pending, user_agent, ip, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at`
	err := r.db.q(ctx).QueryRow(ctx, q, s.ID, s.UserID, s.MFAPending,
		truncate(s.UserAgent, 512), s.IP, s.ExpiresAt).Scan(&s.CreatedAt)
	return mapErr(err)
}

func (r *SessionRepo) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	const q = `SELECT id, user_id, mfa_pending, user_agent, ip, expires_at, created_at
	             FROM sessions WHERE id = $1`
	var s domain.Session
	err := r.db.q(ctx).QueryRow(ctx, q, id).Scan(&s.ID, &s.UserID, &s.MFAPending,
		&s.UserAgent, &s.IP, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

// Promote clears the MFA-pending flag after a TOTP code is verified and
// extends the session to its full lifetime.
func (r *SessionRepo) Promote(ctx context.Context, id string, expiresAt time.Time) error {
	tag, err := r.db.q(ctx).Exec(ctx,
		`UPDATE sessions SET mfa_pending = FALSE, expires_at = $2 WHERE id = $1`, id, expiresAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.q(ctx).Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return mapErr(err)
}

func (r *SessionRepo) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	tag, err := r.db.q(ctx).Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, mapErr(err)
	}
	return int(tag.RowsAffected()), nil
}

var _ domain.SessionRepository = (*SessionRepo)(nil)
