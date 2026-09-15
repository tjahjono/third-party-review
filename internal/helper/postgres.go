package helper

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"third-party-review/internal/config"
)

// ConnectPostgres opens a connection pool against the configured DSN and
// verifies it with a ping, so a bad DATABASE_URL fails at startup with a clear
// message rather than on the first query.
//
// Connecting lives here rather than in the repository package because it is
// infrastructure that any entry point may need - the server, a migration
// command, an integration test - and none of them should have to reach into
// the storage implementation to get a pool. The repository package takes the
// pool it is given.
func ConnectPostgres(ctx context.Context, cfg config.DB) (*pgxpool.Pool, error) {
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
	return pool, nil
}
