package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/database/dbtest"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
)

// These are integration tests: they run against a real PostgreSQL database
// (see internal/database/dbtest) to exercise the actual row-locking and
// transaction behaviour behind Repository.Schedule — in particular the
// concurrency guarantees no fake or mock could prove. They skip cleanly if
// PostgreSQL is not reachable.

func newTestRepository(t *testing.T) (Repository, *sql.DB) {
	t.Helper()
	db := dbtest.Open(t)
	return NewPostgresRepository(db), db
}

// seedNode inserts a Ready node directly (bypassing internal/cluster, which
// this package deliberately does not depend on) with the given capacity,
// and returns its ID.
func seedNode(t *testing.T, db *sql.DB, cpu, memory int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO nodes (id, hostname, status, os, architecture, cpu_capacity, memory_capacity_bytes, agent_version, registered_at, updated_at)
		VALUES ($1, $2, 'Ready', 'linux', 'amd64', $3, $4, '0.1.0', now(), now())`,
		id, "node-"+id.String(), cpu, memory)
	if err != nil {
		t.Fatalf("seeding Ready node: %v", err)
	}
	return id
}

// seedNodeWithStatus inserts a node with an explicit, non-Ready status —
// used to prove Schedule never considers it a candidate.
func seedNodeWithStatus(t *testing.T, db *sql.DB, status string, cpu, memory int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO nodes (id, hostname, status, os, architecture, cpu_capacity, memory_capacity_bytes, agent_version, registered_at, updated_at)
		VALUES ($1, $2, $3, 'linux', 'amd64', $4, $5, '0.1.0', now(), now())`,
		id, "node-"+id.String(), status, cpu, memory)
	if err != nil {
		t.Fatalf("seeding %s node: %v", status, err)
	}
	return id
}

// seedDeployment inserts a deployment row directly and returns its ID.
func seedDeployment(t *testing.T, db *sql.DB, replicas int, cpu, memoryBytes int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
		VALUES ($1, $2, 'nginx:latest', $3, $4, $5, now(), now())`,
		id, "deployment-"+id.String(), replicas, cpu, memoryBytes)
	if err != nil {
		t.Fatalf("seeding deployment: %v", err)
	}
	return id
}

func fixedDecide(d testDeploymentLike) DecideFunc {
	return func(nodes []NodeState, existing []Placement) ([]Placement, error) {
		return decidePlacements(d.deployment(), nodes, existing, time.Now().UTC())
	}
}

// testDeploymentLike lets fixedDecide build a deployment.Deployment without
// this test file importing internal/deployment just for one struct literal
// per call site — see helper below.
type testDeploymentLike struct {
	id       uuid.UUID
	name     string
	replicas int
	cpu      int64
	memory   int64
}

func (d testDeploymentLike) deployment() deployment.Deployment {
	return deployment.Deployment{ID: d.id, Name: d.name, Replicas: d.replicas, CPU: d.cpu, MemoryBytes: d.memory}
}

func TestPostgresRepositoryScheduleSucceedsWithOneReadyNode(t *testing.T) {
	repo, db := newTestRepository(t)
	nodeID := seedNode(t, db, 4, 4096)
	deploymentID := seedDeployment(t, db, 1, 1, 1024)

	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 1, cpu: 1, memory: 1024}
	placements, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(placements))
	}
	if placements[0].NodeID != nodeID {
		t.Errorf("placement landed on node %s, want %s", placements[0].NodeID, nodeID)
	}
}

func TestPostgresRepositoryScheduleUnknownDeploymentReturnsErrDeploymentNotFound(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNode(t, db, 4, 4096)

	d := testDeploymentLike{id: uuid.New(), name: "ghost", replicas: 1, cpu: 1, memory: 1024}
	_, err := repo.Schedule(context.Background(), d.id, fixedDecide(d))
	if !errors.Is(err, ErrDeploymentNotFound) {
		t.Errorf("error = %v, want ErrDeploymentNotFound", err)
	}
}

// Test 3 — NotReady nodes must never be scheduling candidates.
func TestPostgresRepositoryScheduleExcludesNotReadyNodes(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNodeWithStatus(t, db, "NotReady", 4, 4096)
	deploymentID := seedDeployment(t, db, 1, 1, 1024)

	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 1, cpu: 1, memory: 1024}
	_, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Errorf("error = %v, want ErrInsufficientCapacity (the only node is NotReady, so no candidate exists)", err)
	}
}

// Test 4 — Registering nodes must never be scheduling candidates.
func TestPostgresRepositoryScheduleExcludesRegisteringNodes(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNodeWithStatus(t, db, "Registering", 4, 4096)
	deploymentID := seedDeployment(t, db, 1, 1, 1024)

	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 1, cpu: 1, memory: 1024}
	_, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Errorf("error = %v, want ErrInsufficientCapacity (the only node is Registering, so no candidate exists)", err)
	}
}

func TestPostgresRepositoryScheduleReadyAndNotReadyOnlyReadyReceivesPlacement(t *testing.T) {
	repo, db := newTestRepository(t)
	readyID := seedNode(t, db, 4, 4096)
	seedNodeWithStatus(t, db, "NotReady", 4, 4096)
	deploymentID := seedDeployment(t, db, 1, 1, 1024)

	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 1, cpu: 1, memory: 1024}
	placements, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(placements) != 1 || placements[0].NodeID != readyID {
		t.Errorf("placements = %+v, want exactly one placement on the Ready node %s", placements, readyID)
	}
}

// Test 9 (integration) — an existing placement belonging to a DIFFERENT
// deployment must still reduce what's available to a new one; resource
// accounting is cluster-wide, not scoped to one deployment.
func TestPostgresRepositoryScheduleAccountsForOtherDeploymentsAllocations(t *testing.T) {
	repo, db := newTestRepository(t)
	nodeID := seedNode(t, db, 2, 2048) // exactly enough for one 2-CPU/2048-memory replica

	otherDeploymentID := seedDeployment(t, db, 1, 2, 2048)
	other := testDeploymentLike{id: otherDeploymentID, name: "other", replicas: 1, cpu: 2, memory: 2048}
	if _, err := repo.Schedule(context.Background(), otherDeploymentID, fixedDecide(other)); err != nil {
		t.Fatalf("scheduling the first deployment: %v", err)
	}

	// The node's entire capacity is now allocated to "other". A second,
	// unrelated deployment requesting any resources must fail.
	newDeploymentID := seedDeployment(t, db, 1, 1, 1)
	newD := testDeploymentLike{id: newDeploymentID, name: "new", replicas: 1, cpu: 1, memory: 1}
	_, err := repo.Schedule(context.Background(), newDeploymentID, fixedDecide(newD))
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Errorf("error = %v, want ErrInsufficientCapacity — node %s is fully allocated to a different deployment", err, nodeID)
	}
}

// Test 12/19 (integration) — a scheduling attempt that cannot place every
// replica must leave the database with zero new placements, not a partial
// result.
func TestPostgresRepositoryScheduleFailsAtomically(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNode(t, db, 2, 2048) // room for exactly one replica
	deploymentID := seedDeployment(t, db, 2, 2, 2048)

	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 2, cpu: 2, memory: 2048}
	_, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("error = %v, want ErrInsufficientCapacity", err)
	}

	placements, err := repo.ListByDeployment(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("ListByDeployment: %v", err)
	}
	if len(placements) != 0 {
		t.Errorf("found %d placements in the database after a failed Schedule, want 0 (atomic rollback)", len(placements))
	}
}

// Test 13 (integration) — idempotency against a real database.
func TestPostgresRepositoryScheduleIsIdempotent(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNode(t, db, 4, 4096)
	deploymentID := seedDeployment(t, db, 3, 1, 1024)
	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 3, cpu: 1, memory: 1024}

	first, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if err != nil {
		t.Fatalf("first Schedule: %v", err)
	}
	second, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
	if err != nil {
		t.Fatalf("second Schedule: %v", err)
	}
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("first=%d second=%d placements, want 3 and 3", len(first), len(second))
	}

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM deployment_placements WHERE deployment_id = $1`, deploymentID).Scan(&count); err != nil {
		t.Fatalf("counting placements: %v", err)
	}
	if count != 3 {
		t.Errorf("database has %d placement rows after scheduling twice, want exactly 3", count)
	}
}

// Test 14 (integration) — partial scheduling: a deployment already holding
// some replicas gets only the missing ones filled in, and the pre-existing
// rows are untouched (same ID, same CreatedAt).
func TestPostgresRepositoryScheduleFillsOnlyMissingReplicas(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNode(t, db, 4, 4096)
	deploymentID := seedDeployment(t, db, 1, 1, 1024)
	d1 := testDeploymentLike{id: deploymentID, name: "web", replicas: 1, cpu: 1, memory: 1024}

	first, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d1))
	if err != nil {
		t.Fatalf("first Schedule: %v", err)
	}
	originalID := first[0].ID
	originalCreatedAt := first[0].CreatedAt

	// A second node arrives, and the deployment "grows" — in Phase 2.1
	// replicas is immutable via the API, but the repository layer itself
	// makes no such assumption; simulate a larger desired count directly
	// to prove decide only fills the gap.
	seedNode(t, db, 4, 4096)
	d2 := testDeploymentLike{id: deploymentID, name: "web", replicas: 3, cpu: 1, memory: 1024}

	second, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d2))
	if err != nil {
		t.Fatalf("second Schedule: %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("got %d placements, want 3", len(second))
	}

	for _, p := range second {
		if p.ReplicaIndex == 0 {
			if p.ID != originalID {
				t.Errorf("replica 0's placement ID changed from %s to %s — it must never be recreated", originalID, p.ID)
			}
			if !p.CreatedAt.Equal(originalCreatedAt) {
				t.Errorf("replica 0's CreatedAt changed from %v to %v — it must never be recreated", originalCreatedAt, p.CreatedAt)
			}
		}
	}
}

func TestPostgresRepositoryListByDeploymentOrdersByReplicaIndex(t *testing.T) {
	repo, db := newTestRepository(t)
	seedNode(t, db, 8, 8192)
	deploymentID := seedDeployment(t, db, 4, 1, 1024)
	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 4, cpu: 1, memory: 1024}

	if _, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d)); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	placements, err := repo.ListByDeployment(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("ListByDeployment: %v", err)
	}
	if len(placements) != 4 {
		t.Fatalf("got %d placements, want 4", len(placements))
	}
	for i, p := range placements {
		if p.ReplicaIndex != i {
			t.Errorf("placements[%d].ReplicaIndex = %d, want %d (want ascending order)", i, p.ReplicaIndex, i)
		}
	}
}

func TestPostgresRepositoryListByDeploymentUnknownDeploymentReturnsEmpty(t *testing.T) {
	repo, _ := newTestRepository(t)
	placements, err := repo.ListByDeployment(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListByDeployment: %v", err)
	}
	if len(placements) != 0 {
		t.Errorf("got %d placements for an unknown deployment, want 0", len(placements))
	}
}

// Test 22/34 — THE mandatory concurrency test: PostgreSQL's row locking,
// not application logic, must be what makes repeated concurrent scheduling
// of the same deployment safe.
func TestPostgresRepositoryConcurrentScheduleOfSameDeploymentProducesExactlyOnePlacementPerReplica(t *testing.T) {
	repo, db := newTestRepository(t)
	nodeID := seedNode(t, db, 8, 8192) // ample capacity for 3 replicas of 1 CPU/1024 memory
	deploymentID := seedDeployment(t, db, 3, 1, 1024)
	d := testDeploymentLike{id: deploymentID, name: "web", replicas: 3, cpu: 1, memory: 1024}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Schedule(context.Background(), deploymentID, fixedDecide(d))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Schedule call returned an error: %v", err)
		}
	}

	placements, err := repo.ListByDeployment(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("ListByDeployment: %v", err)
	}
	if len(placements) != 3 {
		t.Fatalf("total placements = %d, want exactly 3", len(placements))
	}

	seen := map[int]int{}
	var totalCPU, totalMemory int64
	for _, p := range placements {
		seen[p.ReplicaIndex]++
		if p.NodeID == nodeID {
			totalCPU += p.CPU
			totalMemory += p.MemoryBytes
		}
	}
	for i := 0; i < 3; i++ {
		if seen[i] != 1 {
			t.Errorf("replica %d has %d placement rows, want exactly 1", i, seen[i])
		}
	}
	if totalCPU > 8 {
		t.Errorf("total CPU allocated to node %s = %d, want <= node capacity 8", nodeID, totalCPU)
	}
	if totalMemory > 8192 {
		t.Errorf("total memory allocated to node %s = %d, want <= node capacity 8192", nodeID, totalMemory)
	}
}

// The cross-deployment counterpart: 20 goroutines scheduling 20 DIFFERENT
// single-replica deployments, all competing for the same small node, must
// never over-allocate that node — this is the race described in the Phase
// 2.2 spec (two schedulers both reading "4 CPU available" and both
// reserving 3).
func TestPostgresRepositoryConcurrentScheduleAcrossDeploymentsNeverOverAllocatesNode(t *testing.T) {
	repo, db := newTestRepository(t)
	nodeID := seedNode(t, db, 4, 4096) // room for exactly 4 single-CPU replicas

	const n = 20
	deploymentIDs := make([]uuid.UUID, n)
	for i := range deploymentIDs {
		deploymentIDs[i] = seedDeployment(t, db, 1, 1, 1024)
	}

	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			d := testDeploymentLike{id: id, name: "web", replicas: 1, cpu: 1, memory: 1024}
			_, err := repo.Schedule(context.Background(), id, fixedDecide(d))
			results <- err
		}(deploymentIDs[i])
	}
	wg.Wait()
	close(results)

	var successes, capacityFailures int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInsufficientCapacity):
			capacityFailures++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successes != 4 {
		t.Errorf("successes = %d, want exactly 4 (the node's full 4-CPU capacity, one replica each)", successes)
	}
	if capacityFailures != n-4 {
		t.Errorf("capacity failures = %d, want %d", capacityFailures, n-4)
	}

	var totalCPU, totalMemory int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(SUM(cpu),0), COALESCE(SUM(memory_bytes),0) FROM deployment_placements WHERE node_id = $1`, nodeID,
	).Scan(&totalCPU, &totalMemory); err != nil {
		t.Fatalf("summing allocation: %v", err)
	}
	if totalCPU > 4 {
		t.Errorf("total CPU allocated to node = %d, want <= 4 (node capacity) — over-allocation under concurrency", totalCPU)
	}
	if totalMemory > 4096 {
		t.Errorf("total memory allocated to node = %d, want <= 4096 — over-allocation under concurrency", totalMemory)
	}
}
