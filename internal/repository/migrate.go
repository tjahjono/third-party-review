package repository

import (
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// MigrationsFS is set by the main package to the embedded migrations
// directory. Keeping the embed in cmd/server means this package has no
// compile-time dependency on the repository layout.
type MigrationsFS = embed.FS

// Migrate applies every pending migration in fsys against the pool's database.
// It is safe to call on every startup: already-applied versions are skipped.
func Migrate(pool *pgxpool.Pool, fsys MigrationsFS, dir string, log *slog.Logger) error {
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("migrate: open migration source: %w", err)
	}

	// stdlib.OpenDBFromPool borrows connections from the pgx pool. The
	// golang-migrate driver holds one open for the duration, so it must be
	// closed explicitly via m.Close() - otherwise the borrowed connection is
	// never returned and a later pool.Close() blocks forever waiting for it.
	sqlDB := stdlib.OpenDBFromPool(pool)

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		_ = sqlDB.Close()
		_ = src.Close()
		return fmt.Errorf("migrate: init driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = sqlDB.Close()
		_ = src.Close()
		return fmt.Errorf("migrate: init: %w", err)
	}
	// m.Close() closes both the source and the database driver; the pooled
	// connection is released here.
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Warn("closing migration source failed", "error", srcErr)
		}
		if dbErr != nil {
			log.Warn("closing migration driver failed", "error", dbErr)
		}
		_ = sqlDB.Close()
	}()

	before, dirty, verErr := m.Version()
	if verErr != nil && !errors.Is(verErr, migrate.ErrNilVersion) {
		return fmt.Errorf("migrate: read current version: %w", verErr)
	}
	if dirty {
		return fmt.Errorf("migrate: database is dirty at version %d; resolve manually before starting", before)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}

	after, _, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("migrate: read version: %w", err)
	}
	if before == after {
		log.Info("database schema up to date", "version", after)
	} else {
		log.Info("database schema migrated", "from", before, "to", after)
	}
	return nil
}
