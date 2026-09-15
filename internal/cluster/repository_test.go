package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/database/dbtest"
)

// These are integration tests: they run against a real PostgreSQL database
// (see internal/database/dbtest) to exercise the actual SQL, not a
// hand-written approximation of it — the ON CONFLICT upsert, the RETURNING
// clauses, and the CHECK constraints all need a real server to verify.
// They skip cleanly if PostgreSQL is not reachable.

func newTestRepository(t *testing.T) Repository {
	t.Helper()
	db := dbtest.Open(t)
	return NewPostgresRepository(db)
}

func testNode(hostname string) Node {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return Node{
		ID:                  uuid.New(),
		Hostname:            hostname,
		Status:              StatusReady,
		OS:                  "linux",
		Architecture:        "amd64",
		CPUCapacity:         8,
		MemoryCapacityBytes: 17179869184,
		AgentVersion:        "0.1.0",
		LastHeartbeatAt:     &now,
		RegisteredAt:        now,
		UpdatedAt:           now,
	}
}

func TestPostgresRepositoryUpsertCreatesNewNode(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	node := testNode("worker-01")

	saved, err := repo.Upsert(ctx, node)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.ID != node.ID {
		t.Errorf("ID = %s, want %s", saved.ID, node.ID)
	}
	if saved.Status != StatusReady {
		t.Errorf("Status = %q, want %q", saved.Status, StatusReady)
	}

	fetched, err := repo.Get(ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Hostname != "worker-01" {
		t.Errorf("Hostname = %q, want %q", fetched.Hostname, "worker-01")
	}
}

func TestPostgresRepositoryUpsertPreservesRegisteredAtOnUpdate(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	node := testNode("worker-01")

	first, err := repo.Upsert(ctx, node)
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	updated := node
	updated.Hostname = "worker-01-renamed"
	updated.CPUCapacity = 16
	updated.RegisteredAt = first.RegisteredAt.Add(time.Hour) // must be ignored
	updated.UpdatedAt = first.UpdatedAt.Add(time.Minute)

	second, err := repo.Upsert(ctx, updated)
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	if !second.RegisteredAt.Equal(first.RegisteredAt) {
		t.Errorf("RegisteredAt = %v, want unchanged %v", second.RegisteredAt, first.RegisteredAt)
	}
	if second.Hostname != "worker-01-renamed" {
		t.Errorf("Hostname = %q, want updated value", second.Hostname)
	}
	if second.CPUCapacity != 16 {
		t.Errorf("CPUCapacity = %d, want 16", second.CPUCapacity)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List returned %d rows, want exactly 1 (no duplicate row from the upsert)", len(all))
	}
}

func TestPostgresRepositoryGetUnknownNodeReturnsErrNotFound(t *testing.T) {
	repo := newTestRepository(t)

	_, err := repo.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryListOrdersByHostname(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	for _, hostname := range []string{"charlie", "alice", "bob"} {
		if _, err := repo.Upsert(ctx, testNode(hostname)); err != nil {
			t.Fatalf("Upsert(%s): %v", hostname, err)
		}
	}

	nodes, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("List returned %d nodes, want 3", len(nodes))
	}
	want := []string{"alice", "bob", "charlie"}
	for i, hostname := range want {
		if nodes[i].Hostname != hostname {
			t.Errorf("nodes[%d].Hostname = %q, want %q", i, nodes[i].Hostname, hostname)
		}
	}
}

func TestPostgresRepositoryRecordHeartbeatUpdatesTimestampsAndStatus(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	node := testNode("worker-01")
	node.Status = StatusNotReady // so previousStatus is verifiably captured below
	if _, err := repo.Upsert(ctx, node); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	receivedAt := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	updated, previous, err := repo.RecordHeartbeat(ctx, node.ID, receivedAt)
	if err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	if updated.LastHeartbeatAt == nil || !updated.LastHeartbeatAt.Equal(receivedAt) {
		t.Errorf("LastHeartbeatAt = %v, want %v", updated.LastHeartbeatAt, receivedAt)
	}
	if !updated.UpdatedAt.Equal(receivedAt) {
		t.Errorf("UpdatedAt = %v, want %v", updated.UpdatedAt, receivedAt)
	}
	if updated.Status != StatusReady {
		t.Errorf("Status = %q, want %q", updated.Status, StatusReady)
	}
	if previous != StatusNotReady {
		t.Errorf("previousStatus = %q, want %q", previous, StatusNotReady)
	}
}

func TestPostgresRepositoryRecordHeartbeatUnknownNodeReturnsErrNotFound(t *testing.T) {
	repo := newTestRepository(t)

	_, _, err := repo.RecordHeartbeat(context.Background(), uuid.New(), time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryMarkStaleOnlyAffectsOverdueReadyNodes(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)

	overdue := testNode("overdue")
	oldHeartbeat := base.Add(-time.Hour)
	overdue.LastHeartbeatAt = &oldHeartbeat
	if _, err := repo.Upsert(ctx, overdue); err != nil {
		t.Fatalf("Upsert(overdue): %v", err)
	}

	fresh := testNode("fresh")
	recentHeartbeat := base.Add(-time.Second)
	fresh.LastHeartbeatAt = &recentHeartbeat
	if _, err := repo.Upsert(ctx, fresh); err != nil {
		t.Fatalf("Upsert(fresh): %v", err)
	}

	alreadyDown := testNode("already-notready")
	alreadyDown.Status = StatusNotReady
	oldHeartbeat2 := base.Add(-time.Hour)
	alreadyDown.LastHeartbeatAt = &oldHeartbeat2
	if _, err := repo.Upsert(ctx, alreadyDown); err != nil {
		t.Fatalf("Upsert(already-notready): %v", err)
	}

	cutoff := base.Add(-time.Minute)
	stale, err := repo.MarkStale(ctx, cutoff, base)
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	if len(stale) != 1 {
		t.Fatalf("MarkStale returned %d nodes, want 1 (only 'overdue')", len(stale))
	}
	if stale[0].Hostname != "overdue" {
		t.Errorf("stale node = %q, want %q", stale[0].Hostname, "overdue")
	}
	if stale[0].Status != StatusNotReady {
		t.Errorf("Status = %q, want %q", stale[0].Status, StatusNotReady)
	}

	fetchedFresh, err := repo.Get(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("Get(fresh): %v", err)
	}
	if fetchedFresh.Status != StatusReady {
		t.Errorf("fresh node Status = %q, want %q (untouched)", fetchedFresh.Status, StatusReady)
	}
}

// A NULL last_heartbeat_at cannot occur through Service.Register (which
// always sets it) or RecordHeartbeat, but the column is nullable at the
// schema level, so MarkStale must still treat a NULL as "definitely
// overdue" rather than silently exempting such a row from failure detection
// forever — see the NULL branch added to MarkStale's WHERE clause.
func TestPostgresRepositoryMarkStaleTreatsNullHeartbeatAsStale(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := testNode("null-heartbeat")
	node.LastHeartbeatAt = nil // Upsert writes this straight through as SQL NULL
	saved, err := repo.Upsert(ctx, node)
	if err != nil {
		t.Fatalf("Upsert with a nil LastHeartbeatAt: %v", err)
	}
	if saved.LastHeartbeatAt != nil {
		t.Fatalf("precondition failed: saved.LastHeartbeatAt = %v, want nil", saved.LastHeartbeatAt)
	}

	now := time.Now().UTC()
	// An implausibly permissive cutoff: if NULL is handled correctly, this
	// node is stale no matter how far in the future the cutoff is.
	stale, err := repo.MarkStale(ctx, now.Add(100*365*24*time.Hour), now)
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	found := false
	for _, n := range stale {
		if n.ID == node.ID {
			found = true
		}
	}
	if !found {
		t.Error("MarkStale did not mark a Ready node with a NULL last_heartbeat_at as stale")
	}

	fetched, err := repo.Get(ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Status != StatusNotReady {
		t.Errorf("Status = %q, want %q", fetched.Status, StatusNotReady)
	}
}

// Registration is idempotent-by-ID at the database layer too: N concurrent
// Upserts for the same ID against a real PostgreSQL instance must converge
// on exactly one row, not create duplicates or error out under contention.
func TestPostgresRepositoryConcurrentUpsertSameIDConverges(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := uuid.New()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node := testNode("concurrent-node")
			node.ID = id
			if _, err := repo.Upsert(ctx, node); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Upsert: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List returned %d rows after %d concurrent upserts of the same ID, want 1", len(all), n)
	}
}

func TestPostgresRepositoryRejectsInvalidResourceCapacity(t *testing.T) {
	repo := newTestRepository(t)
	node := testNode("bad-capacity")
	node.CPUCapacity = 0

	_, err := repo.Upsert(context.Background(), node)
	if err == nil {
		t.Fatal("Upsert with zero CPU capacity succeeded, want the CHECK constraint to reject it")
	}
}
