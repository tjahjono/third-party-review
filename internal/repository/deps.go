package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Step 1 - Define the Dependency Interface.
//
// Every repository in this package needs exactly one thing: somewhere to run a
// query. Naming that as an interface rather than taking the concrete *DB means
// a repository can be pointed at a pool, at an open transaction, or at a fake
// in a test, without any of them knowing about the others.

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
