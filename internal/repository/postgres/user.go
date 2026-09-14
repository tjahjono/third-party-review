package postgres

import (
	"context"
	"strings"
	"time"

	"third-party-review/internal/domain"
)

// UserRepo is the PostgreSQL implementation of domain.UserRepository.
type UserRepo struct{ db *DB }

const userCols = `
	id, username, display_name, password_hash, mfa_enabled, mfa_secret,
	mfa_enrolled_at, COALESCE(array_length(mfa_recovery_codes, 1), 0) > 0,
	last_login_at, created_at, updated_at`

func scanUser(s interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	if err := s.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.MFAEnabled, &u.MFASecret, &u.MFAEnrolledAt, &u.MFARecoveryHas,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" || u.PasswordHash == "" {
		return domain.ValidationError{Field: "username", Message: "Username and password are required."}
	}
	const q = `
		INSERT INTO users (username, display_name, password_hash, mfa_enabled, mfa_secret)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, created_at, updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, u.Username, u.DisplayName, u.PasswordHash, u.MFAEnabled, u.MFASecret).
		Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	return mapErr(err)
}

func (r *UserRepo) Update(ctx context.Context, u *domain.User) error {
	const q = `
		UPDATE users
		   SET display_name = $2, password_hash = $3, mfa_enabled = $4,
		       mfa_secret = $5, mfa_enrolled_at = $6
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, u.ID, u.DisplayName, u.PasswordHash,
		u.MFAEnabled, u.MFASecret, u.MFAEnrolledAt).Scan(&u.UpdatedAt)
	return mapErr(err)
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return scanUser(r.db.q(ctx).QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return scanUser(r.db.q(ctx).QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username) = lower($1)`, strings.TrimSpace(username)))
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.q(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, mapErr(err)
}

func (r *UserRepo) RecordLogin(ctx context.Context, id int64, at time.Time) error {
	_, err := r.db.q(ctx).Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, id, nullTime(at))
	return mapErr(err)
}

var _ domain.UserRepository = (*UserRepo)(nil)

// RecoveryCodes returns the bcrypt hashes of the user's unused MFA recovery
// codes.
func (r *UserRepo) RecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	var codes []string
	err := r.db.q(ctx).QueryRow(ctx,
		`SELECT mfa_recovery_codes FROM users WHERE id = $1`, userID).Scan(&codes)
	if err != nil {
		return nil, mapErr(err)
	}
	return codes, nil
}

// ReplaceRecoveryCodes swaps the whole set atomically. A nil slice clears them.
func (r *UserRepo) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	if hashes == nil {
		hashes = []string{}
	}
	tag, err := r.db.q(ctx).Exec(ctx,
		`UPDATE users SET mfa_recovery_codes = $2 WHERE id = $1`, userID, hashes)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ConsumeRecoveryCode removes one code from the set. array_remove makes this a
// single statement, so a code cannot be used twice by two concurrent requests.
func (r *UserRepo) ConsumeRecoveryCode(ctx context.Context, userID int64, hash string) error {
	tag, err := r.db.q(ctx).Exec(ctx,
		`UPDATE users SET mfa_recovery_codes = array_remove(mfa_recovery_codes, $2)
		  WHERE id = $1 AND $2 = ANY(mfa_recovery_codes)`, userID, hash)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		// Someone else consumed it first.
		return domain.ErrNotFound
	}
	return nil
}
