package database_test

import (
	"context"
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

	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate call: %v", err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("third Migrate call: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations rows: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations has %d rows after 3 Migrate calls, want 1 (each migration recorded exactly once)", count)
	}
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
