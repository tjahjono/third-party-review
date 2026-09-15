package helper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"third-party-review/internal/config"
)

// Querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx, so a query
// written once runs identically inside or outside a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
}

// ConnProvider hands a repository the right Querier for the current context.
//
// The indirection is what makes transactions invisible to repository code: the
// provider returns the transaction carried on the context when there is one and
// the pool otherwise, so no repository method needs a transaction-aware variant
// and no caller has to remember to use it.
type ConnProvider interface {
	Querier(ctx context.Context) Querier
}

// DB owns the connection pool and satisfies both ConnProvider and TxManager,
// so every repository is built from the same value that runs its
// transactions.
type DB struct {
	pool *pgxpool.Pool
}

// New wraps an already-open pool, for a caller that already has one (an
// integration test, a migration command) rather than dialling a second time.
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Connect opens a pool against the configured DSN and wraps it, for the
// common case where the caller just wants a working DB from config.
func Connect(ctx context.Context, cfg config.DB) (*DB, error) {
	pool, err := ConnectPostgres(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return New(pool), nil
}

// Pool exposes the underlying pool for migration tooling and health checks.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Close releases every pooled connection.
func (d *DB) Close() { d.pool.Close() }

// Ping verifies the database is reachable, for the health endpoint.
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

type txKey struct{}

// Querier satisfies ConnProvider: it returns the transaction carried on the
// context when there is one, and the pool otherwise. Repositories therefore
// never branch on whether they are inside a transaction.
func (d *DB) Querier(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok && tx != nil {
		return tx
	}
	return d.pool
}

var _ ConnProvider = (*DB)(nil)

// RunInTx executes fn inside a transaction, committing on success and rolling
// back on error or panic. Nested calls join the outer transaction rather than
// opening a second one.
func (d *DB) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("helper: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("helper: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("helper: commit: %w", err)
	}
	return nil
}

var _ TxManager = (*DB)(nil)

// MapErr converts driver errors into the sentinel errors declared in this
// package, so no layer above repository needs to know pgx exists.
func MapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", ErrAlreadyExists, pgErr.ConstraintName)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: referenced record does not exist (%s)", ErrInvalidInput, pgErr.ConstraintName)
		case "23514": // check_violation
			return fmt.Errorf("%w: value rejected by %s", ErrInvalidInput, pgErr.ConstraintName)
		case "40001", "40P01": // serialization_failure, deadlock_detected
			return fmt.Errorf("%w: concurrent update, retry", ErrConflict)
		}
	}
	return err
}

// NullTime normalises a zero time to NULL on write.
func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// LikeArg builds a case-insensitive contains pattern for ILIKE searches,
// escaping the wildcards a user may have typed.
func LikeArg(search string) string {
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
