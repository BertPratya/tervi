package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var (
	// ErrNotMigrated means the database has no applied migrations.
	ErrNotMigrated = errors.New(`database is not migrated: run "server migrate" first`)
	// ErrVersionMismatch marks a database version this program cannot use.
	ErrVersionMismatch = errors.New("database version mismatch")
)

// Migrate applies the embedded PostgreSQL migrations and returns the database version.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	provider, err := newProvider(pool, true)
	if err != nil {
		return 0, err
	}
	defer provider.Close()

	if _, err := provider.Up(ctx); err != nil {
		return 0, fmt.Errorf("apply database migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read database migration version: %w", err)
	}
	return version, nil
}

// CheckVersion compares the database's applied version with this program's migrations.
func CheckVersion(ctx context.Context, pool *pgxpool.Pool) error {
	var versionTableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&versionTableExists); err != nil {
		return fmt.Errorf("check database migration table: %w", err)
	}
	if !versionTableExists {
		return ErrNotMigrated
	}

	provider, err := newProvider(pool, false)
	if err != nil {
		return err
	}
	defer provider.Close()

	databaseVersion, newestVersion, err := provider.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("read database migration versions: %w", err)
	}
	return versionError(databaseVersion, newestVersion)
}

func versionError(databaseVersion, newestVersion int64) error {
	if databaseVersion == 0 {
		return ErrNotMigrated
	}
	if databaseVersion < newestVersion {
		return versionMismatchError{message: fmt.Sprintf(
			"database is at version %d, this program needs version %d: run \"server migrate\" first",
			databaseVersion, newestVersion,
		)}
	}
	if databaseVersion > newestVersion {
		return versionMismatchError{message: fmt.Sprintf(
			"database is at version %d, newer than this program (version %d): use a newer program",
			databaseVersion, newestVersion,
		)}
	}
	return nil
}

type versionMismatchError struct {
	message string
}

func (err versionMismatchError) Error() string { return err.message }

func (versionMismatchError) Unwrap() error { return ErrVersionMismatch }

func newProvider(pool *pgxpool.Pool, useSessionLocker bool) (*goose.Provider, error) {
	database := stdlib.OpenDB(*pool.Config().ConnConfig)

	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	options := []goose.ProviderOption{goose.WithVerbose(false)}
	if useSessionLocker {
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("create migration session locker: %w", err)
		}
		options = append(options, goose.WithSessionLocker(locker))
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations, options...)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
