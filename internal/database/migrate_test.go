package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dhananjaiyadav1234/Nimbus/internal/database"
	"github.com/dhananjaiyadav1234/Nimbus/internal/database/dbtest"
)

// Migrate is exercised indirectly by every dbtest.Open call across the
// codebase (it runs migrations before handing back the connection), but
// these tests target its own contract directly: idempotent re-runs and a
// fresh database ending up with the expected schema.

func TestMigrateIsSafeToRunRepeatedly(t *testing.T) {
	db := dbtest.Open(t) // already migrated once by Open
	ctx := context.Background()

	before := countSchemaMigrations(t, db)
	if before == 0 {
		t.Fatal("schema_migrations is empty after the initial migration, want at least one row")
	}

	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate call: %v", err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("third Migrate call: %v", err)
	}

	// Not a hard-coded count: this only asserts re-running Migrate doesn't
	// re-record (or lose) any migration, whatever the current number of
	// migration files happens to be.
	after := countSchemaMigrations(t, db)
	if after != before {
		t.Errorf("schema_migrations has %d rows after 3 Migrate calls, want %d (each migration recorded exactly once, unchanged by repeated calls)", after, before)
	}
}

func countSchemaMigrations(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations rows: %v", err)
	}
	return count
}

func TestMigrateCreatesTheNodesTable(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	// A trivial query against every documented column proves the table and
	// its columns exist with compatible types, without hard-coding the
	// database's internal information_schema representation.
	_, err := db.ExecContext(ctx, `
		SELECT id, hostname, status, os, architecture, cpu_capacity,
		       memory_capacity_bytes, agent_version, last_heartbeat_at,
		       registered_at, updated_at
		FROM nodes
		WHERE false
	`)
	if err != nil {
		t.Fatalf("querying nodes table columns: %v", err)
	}
}

func TestMigrateEnforcesStatusCheckConstraint(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		INSERT INTO nodes (id, hostname, status, os, architecture, cpu_capacity, memory_capacity_bytes, agent_version, registered_at, updated_at)
		VALUES (gen_random_uuid(), 'bad', 'NotAStatus', 'linux', 'amd64', 1, 1, '0.1.0', now(), now())
	`)
	if err == nil {
		t.Fatal("insert with an invalid status succeeded, want the CHECK constraint to reject it")
	}
}

func TestMigrateCreatesTheDeploymentsTable(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		SELECT id, name, image, replicas, cpu, memory_bytes, created_at, updated_at
		FROM deployments
		WHERE false
	`)
	if err != nil {
		t.Fatalf("querying deployments table columns: %v", err)
	}
}

func TestMigrateEnforcesDeploymentsConstraints(t *testing.T) {
	tests := map[string]string{
		"negative replicas": `INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
			VALUES (gen_random_uuid(), 'bad-replicas', 'nginx', -1, 1, 1, now(), now())`,
		"zero cpu": `INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
			VALUES (gen_random_uuid(), 'bad-cpu', 'nginx', 1, 0, 1, now(), now())`,
		"zero memory": `INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
			VALUES (gen_random_uuid(), 'bad-memory', 'nginx', 1, 1, 0, now(), now())`,
	}

	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			db := dbtest.Open(t)
			if _, err := db.ExecContext(context.Background(), query); err == nil {
				t.Fatal("insert succeeded, want the CHECK constraint to reject it")
			}
		})
	}
}

func TestMigrateEnforcesDeploymentsNameUniqueness(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()
	const insert = `INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
		VALUES (gen_random_uuid(), 'dup', 'nginx', 1, 1, 1, now(), now())`

	if _, err := db.ExecContext(ctx, insert); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert); err == nil {
		t.Fatal("second insert with a duplicate name succeeded, want the UNIQUE constraint to reject it")
	}
}
