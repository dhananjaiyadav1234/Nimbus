package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// migrationsFS embeds every migration file directly into the binary, so
// deploying Nimbus never requires shipping SQL files alongside it.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies every migration under internal/database/migrations that
// has not yet been recorded in the schema_migrations table, in filename
// order, each inside its own transaction. It is safe to call on every
// startup: a fresh database gets the full schema, and a database that
// already has it sees no-op re-runs of already-applied versions skipped.
//
// A PostgreSQL advisory lock serialises this against any other Nimbus
// process migrating the same database concurrently (for example two Control
// Plane instances starting at once); Phase 1.2 does not require multiple
// Control Planes, but the lock is one line and removes the failure mode
// entirely, so it is included.
func Migrate(ctx context.Context, db *sql.DB) error {
	names, err := migrationFilenames()
	if err != nil {
		return fmt.Errorf("database: reading embedded migrations: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("database: acquiring migration connection: %w", err)
	}
	defer conn.Close()

	// A fixed, arbitrary 64-bit key naming Nimbus's migration lock. Any two
	// Nimbus processes migrating the same database serialise on this key;
	// it has no meaning outside that.
	const advisoryLockKey = 0x4e494d425553 // "NIMBUS" in hex, truncated to fit
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("database: acquiring migration lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("database: creating schema_migrations table: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("database: reading applied migrations: %w", err)
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return fmt.Errorf("database: reading applied migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("database: reading applied migrations: %w", err)
	}
	rows.Close()

	for _, name := range names {
		if applied[name] {
			continue
		}

		sqlText, err := migrationsFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("database: reading migration %s: %w", name, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("database: starting transaction for migration %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, string(sqlText)); err != nil {
			tx.Rollback()
			return fmt.Errorf("database: applying migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("database: recording migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("database: committing migration %s: %w", name, err)
		}
	}

	return nil
}

// migrationFilenames returns every embedded migration's filename, sorted so
// that "0001_..." always runs before "0002_...".
func migrationFilenames() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}
