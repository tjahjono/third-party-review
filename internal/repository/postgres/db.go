// Package postgres contains PostgreSQL implementations of the repository
// interfaces declared in internal/domain. Nothing above this package imports
// pgx, so the storage engine can be swapped without touching service logic.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"third-party-review/internal/config"
	"third-party-review/internal/domain"
)

// DB owns the connection pool and hands out repository implementations.
type DB struct {
	pool *pgxpool.Pool
}

// Connect opens and verifies a pool against the configured DSN.
func Connect(ctx context.Context, cfg config.DB) (*DB, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DATABASE_URL: %w", err)
	}
	pc.MaxConns = cfg.MaxConns
	pc.MinConns = cfg.MinConns
	pc.MaxConnLifetime = cfg.MaxConnLifetime
	pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Pool exposes the underlying pool for migration tooling and health checks.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Close releases every pooled connection.
func (d *DB) Close() { d.pool.Close() }

// Ping verifies the database is reachable, for the health endpoint.
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// Repositories returns the full repository set backed by this pool.
func (d *DB) Repositories() *domain.Repositories {
	return &domain.Repositories{
		Tx:          d,
		Vendors:     &VendorRepo{db: d},
		Domains:     &DomainRepo{db: d},
		Assessments: &AssessmentRepo{db: d},
		Questions:   &QuestionRepo{db: d},
		Results:     &ReviewResultRepo{db: d},
		Summaries:   &SummaryRepo{db: d},
		Rubrics:     &RubricRepo{db: d},
		Jobs:        &JobRepo{db: d},
		Users:       &UserRepo{db: d},
		Sessions:    &SessionRepo{db: d},
	}
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

type txKey struct{}

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx, so every
// query in this package works identically inside or outside a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
}

// q returns the transaction carried on the context, or the pool.
func (d *DB) q(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok && tx != nil {
		return tx
	}
	return d.pool
}

// RunInTx executes fn inside a transaction, committing on success and rolling
// back on error or panic. Nested calls join the outer transaction rather than
// opening a second one.
func (d *DB) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("postgres: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// mapErr converts driver errors into the sentinel errors declared in domain,
// so no layer above this package needs to know pgx exists.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, pgErr.ConstraintName)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: referenced record does not exist (%s)", domain.ErrInvalidInput, pgErr.ConstraintName)
		case "23514": // check_violation
			return fmt.Errorf("%w: value rejected by %s", domain.ErrInvalidInput, pgErr.ConstraintName)
		case "40001", "40P01": // serialization_failure, deadlock_detected
			return fmt.Errorf("%w: concurrent update, retry", domain.ErrConflict)
		}
	}
	return err
}

// nullTime normalises a zero time to NULL on write.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// likeArg builds a case-insensitive contains pattern for ILIKE searches,
// escaping the wildcards a user may have typed.
func likeArg(search string) string {
	escaped := make([]rune, 0, len(search)+8)
	for _, r := range search {
		switch r {
		case '%', '_', '\\':
			escaped = append(escaped, '\\', r)
		default:
			escaped = append(escaped, r)
		}
	}
	return "%" + string(escaped) + "%"
}
