package repository

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type UserRepository interface {
	Create(ctx context.Context, u *model.User) error
	Update(ctx context.Context, u *model.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	// List returns every account, active or not, for the user management
	// screen. There is no pagination: this is one internal team, not a
	// customer base.
	List(ctx context.Context) ([]*model.User, error)
	Count(ctx context.Context) (int, error)
	// ActiveCount backs the "at least one active account" rule that stops the
	// last sign-in from being deactivated.
	ActiveCount(ctx context.Context) (int, error)
	RecordLogin(ctx context.Context, id uuid.UUID, at time.Time) error
}

type userRepository struct {
	db helper.ConnProvider
}

func NewUserRepository(db helper.ConnProvider) UserRepository {
	return &userRepository{db: db}
}

const userColumns = `
	id, username, display_name, password_hash, active, mfa_enabled, mfa_secret,
	mfa_enrolled_at,
	EXISTS (SELECT 1 FROM mfa_recovery_codes c WHERE c.user_id = users.id),
	language,
	last_login_at, created_at, updated_at`

func scanUser(s interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	if err := s.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Active,
		&u.MFAEnabled, &u.MFASecret, &u.MFAEnrolledAt, &u.MFARecoveryHas, &u.Language,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, helper.MapErr(err)
	}
	return &u, nil
}

func (r *userRepository) Create(ctx context.Context, u *model.User) error {
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" || u.PasswordHash == "" {
		return helper.ValidationError{Field: "username", Message: "Username and password are required."}
	}
	if u.Language == "" {
		u.Language = model.LanguageEnglish
	}
	const q = `
		INSERT INTO users (username, display_name, password_hash, mfa_enabled, mfa_secret, language)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, active, created_at, updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, u.Username, u.DisplayName, u.PasswordHash, u.MFAEnabled, u.MFASecret, string(u.Language)).
		Scan(&u.ID, &u.Active, &u.CreatedAt, &u.UpdatedAt)
	return helper.MapErr(err)
}

func (r *userRepository) Update(ctx context.Context, u *model.User) error {
	u.Username = strings.TrimSpace(u.Username)
	const q = `
		UPDATE users
		   SET username = $2, display_name = $3, password_hash = $4, active = $5,
		       mfa_enabled = $6, mfa_secret = $7, mfa_enrolled_at = $8, language = $9
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, u.ID, u.Username, u.DisplayName, u.PasswordHash, u.Active,
		u.MFAEnabled, u.MFASecret, u.MFAEnrolledAt, string(u.Language)).Scan(&u.UpdatedAt)
	return helper.MapErr(err)
}

func (r *userRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return scanUser(r.db.Querier(ctx).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE users.id = $1`, id))
}

func (r *userRepository) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(r.db.Querier(ctx).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(username) = lower($1)`, strings.TrimSpace(username)))
}

// List orders by username, which is the order the user management screen
// shows accounts in.
func (r *userRepository) List(ctx context.Context) ([]*model.User, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY lower(username)`)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *userRepository) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, helper.MapErr(err)
}

func (r *userRepository) ActiveCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE active`).Scan(&n)
	return n, helper.MapErr(err)
}

func (r *userRepository) RecordLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, id, helper.NullTime(at))
	return helper.MapErr(err)
}
