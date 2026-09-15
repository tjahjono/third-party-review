package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
)

type RecoveryCodeRepository interface {
	ListHashes(ctx context.Context, userID uuid.UUID) ([]string, error)
	Replace(ctx context.Context, userID uuid.UUID, hashes []string) error
	Consume(ctx context.Context, userID uuid.UUID, hash string) error
}

type recoveryCodeRepository struct {
	db helper.ConnProvider
}

func NewRecoveryCodeRepository(db helper.ConnProvider) RecoveryCodeRepository {
	return &recoveryCodeRepository{db: db}
}

// ListHashes returns the bcrypt hashes of the user's unused recovery codes.
func (r *recoveryCodeRepository) ListHashes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT code_hash FROM mfa_recovery_codes WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, h)
	}
	return out, helper.MapErr(rows.Err())
}

// Replace swaps the whole set. A nil slice clears them. Both statements run in
// whatever transaction the context carries, so a caller that wraps this in one
// never leaves a user with no codes at all.
func (r *recoveryCodeRepository) Replace(ctx context.Context, userID uuid.UUID, hashes []string) error {
	q := r.db.Querier(ctx)
	if _, err := q.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return helper.MapErr(err)
	}
	for _, h := range hashes {
		if _, err := q.Exec(ctx,
			`INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return helper.MapErr(err)
		}
	}
	return nil
}

// Consume removes one code. The delete is the check: if it affects no rows the
// code was already spent, so a replay loses the race rather than succeeding.
func (r *recoveryCodeRepository) Consume(ctx context.Context, userID uuid.UUID, hash string) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM mfa_recovery_codes WHERE user_id = $1 AND code_hash = $2`, userID, hash)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}
