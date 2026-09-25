package scheduler

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
)

// These tests exercise decidePlacements directly — the pure scheduling
// algorithm, with no database, no HTTP, no CLI. "Ready nodes only" (tests
// matching numbers 3/4 in the Phase 2.2 spec) is proven at the integration
// layer instead (see repository_test.go's
// TestPostgresRepositoryScheduleOnlyConsidersReadyNodes): decidePlacements
// itself has no concept of node status at all — NodeState carries none —
// because Ready-filtering happens once, in SQL, before any node ever
// reaches this package. That is itself the guarantee: a NotReady or
// Registering node's capacity is structurally unavailable to this
// algorithm, not merely filtered by a check it could get wrong.

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newNode(cpu, memory int64) NodeState {
	return NodeState{NodeID: uuid.New(), CPUCapacity: cpu, MemoryCapacity: memory}
}

func testDeployment(replicas int, cpu, memoryBytes int64) deployment.Deployment {
	return deployment.Deployment{ID: uuid.New(), Name: "web", Image: "nginx:latest", Replicas: replicas, CPU: cpu, MemoryBytes: memoryBytes}
}

// Test 1 — No Ready nodes.
func TestDecidePlacementsNoNodesFails(t *testing.T) {
	d := testDeployment(1, 1, 1024)
	_, err := decidePlacements(d, nil, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements with zero nodes succeeded, want ErrInsufficientCapacity")
	}
	assertWrapsInsufficientCapacity(t, err)
}

// Test 2 — One Ready node, one replica.
func TestDecidePlacementsOneNodeOneReplicaSucceeds(t *testing.T) {
	node := newNode(4, 4096)
	d := testDeployment(1, 1, 1024)

	placements, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements: %v", err)
	}
	if len(placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(placements))
	}
	if placements[0].NodeID != node.NodeID || placements[0].ReplicaIndex != 0 {
		t.Errorf("placement = %+v, want ReplicaIndex=0 NodeID=%s", placements[0], node.NodeID)
	}
	if placements[0].DeploymentID != d.ID || placements[0].CPU != 1 || placements[0].MemoryBytes != 1024 {
		t.Errorf("placement resource fields wrong: %+v", placements[0])
	}
}

// Test 5 — CPU insufficient: candidate rejected.
func TestDecidePlacementsRejectsNodeWithInsufficientCPU(t *testing.T) {
	node := newNode(1, 4096) // plenty of memory, not enough CPU
	d := testDeployment(1, 2, 1024)

	_, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded despite insufficient CPU, want ErrInsufficientCapacity")
	}
	assertWrapsInsufficientCapacity(t, err)
}

// Test 6 — Memory insufficient: candidate rejected.
func TestDecidePlacementsRejectsNodeWithInsufficientMemory(t *testing.T) {
	node := newNode(4, 512) // plenty of CPU, not enough memory
	d := testDeployment(1, 1, 1024)

	_, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded despite insufficient memory, want ErrInsufficientCapacity")
	}
	assertWrapsInsufficientCapacity(t, err)
}

// Test 7 — Exact fit succeeds.
func TestDecidePlacementsExactFitSucceeds(t *testing.T) {
	node := newNode(1, 1024)
	d := testDeployment(1, 1, 1024)

	placements, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements with an exact-fit node failed: %v", err)
	}
	if len(placements) != 1 || placements[0].NodeID != node.NodeID {
		t.Errorf("placements = %+v, want exactly one placement on the exact-fit node", placements)
	}
}

// Test 8 — Multiple replicas: resource accounting must change after each
// simulated placement, not be recalculated independently from the
// original state.
func TestDecidePlacementsMultipleReplicasUpdateWorkingState(t *testing.T) {
	node := newNode(4, 4096) // room for exactly 2 replicas of 2 CPU/2048 memory each
	d := testDeployment(3, 2, 2048)

	_, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded fitting 3 replicas of 2 CPU each onto a 4 CPU node, want failure (only 2 fit)")
	}
	assertWrapsInsufficientCapacity(t, err)

	// Exactly 2 replicas must fit.
	d2 := testDeployment(2, 2, 2048)
	placements, err := decidePlacements(d2, []NodeState{node}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements for 2 replicas of 2 CPU each on a 4 CPU node: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}
}

func TestDecidePlacementsMultipleReplicasAcrossTwoNodes(t *testing.T) {
	nodeA := newNode(4, 4096) // capacity for CPU=4
	nodeB := newNode(4, 4096)
	d := testDeployment(3, 2, 2048) // 3 replicas of 2 CPU/2048 memory: A can hold 2, B holds 1

	placements, err := decidePlacements(d, []NodeState{nodeA, nodeB}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements: %v", err)
	}
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}

	perNode := map[uuid.UUID]int{}
	for _, p := range placements {
		perNode[p.NodeID]++
	}
	total := perNode[nodeA.NodeID] + perNode[nodeB.NodeID]
	if total != 3 {
		t.Fatalf("placements landed on unexpected nodes: %+v", perNode)
	}
	// Neither node may have been asked to hold more than its capacity
	// allows (2 replicas of 2 CPU = 4 CPU, exactly the cap).
	if perNode[nodeA.NodeID] > 2 || perNode[nodeB.NodeID] > 2 {
		t.Errorf("a node was over-subscribed: %+v", perNode)
	}
}

// Test 9 — Existing (already-persisted) allocation on a node — from this or
// any other deployment — must reduce what's available to a new placement.
// decidePlacements itself receives NodeState already carrying that
// allocation (Repository.Schedule computes it cluster-wide before calling
// decide — see repository.go and TestPostgresRepositoryScheduleAccountsForOtherDeploymentsAllocations
// in repository_test.go for the integration-level proof); this test proves
// decidePlacements honours it once given.
func TestDecidePlacementsHonoursPreExistingAllocation(t *testing.T) {
	node := NodeState{NodeID: uuid.New(), CPUCapacity: 4, MemoryCapacity: 4096, AllocatedCPU: 3, AllocatedMemory: 1024}
	// Only 1 CPU / 3072 memory left. A request for 2 CPU must fail.
	d := testDeployment(1, 2, 1024)

	_, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded despite pre-existing allocation leaving insufficient CPU, want failure")
	}
	assertWrapsInsufficientCapacity(t, err)
}

// Test 10 — Determinism: identical inputs must produce identical outputs,
// run repeatedly.
func TestDecidePlacementsIsDeterministic(t *testing.T) {
	nodeA := newNode(2, 2048)
	nodeB := newNode(2, 2048)
	nodeC := newNode(2, 2048)
	d := testDeployment(2, 1, 1024)

	var first []Placement
	for i := 0; i < 20; i++ {
		nodes := []NodeState{nodeA, nodeB, nodeC} // same slice contents every time, fresh copy each call inside decidePlacements
		got, err := decidePlacements(d, nodes, nil, fixedNow)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		if !placementsEqual(first, got) {
			t.Fatalf("run %d produced a different result than run 0:\nrun0=%+v\nrun%d=%+v", i, first, i, got)
		}
	}
}

// Test 11 — Deterministic tie-break: when candidates score identically,
// the same node must always be chosen — ascending node ID order.
func TestDecidePlacementsTieBreaksByAscendingNodeID(t *testing.T) {
	// Two nodes with identical capacity produce an identical fitScore for
	// the same request — a pure tie.
	a := newNode(4, 4096)
	b := newNode(4, 4096)
	d := testDeployment(1, 1, 1024)

	// Whichever of a/b has the lexicographically smaller UUID string must
	// win, regardless of which order they're passed in.
	var want uuid.UUID
	if a.NodeID.String() < b.NodeID.String() {
		want = a.NodeID
	} else {
		want = b.NodeID
	}

	forward, err := decidePlacements(d, []NodeState{a, b}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements (forward order): %v", err)
	}
	reverse, err := decidePlacements(d, []NodeState{b, a}, nil, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements (reverse order): %v", err)
	}

	if forward[0].NodeID != want {
		t.Errorf("forward order picked node %s, want %s (ascending tie-break)", forward[0].NodeID, want)
	}
	if reverse[0].NodeID != want {
		t.Errorf("reverse order picked node %s, want %s — input order must not affect the tie-break", reverse[0].NodeID, want)
	}
}

// Test 12 — Insufficient capacity leaves no partial placements: if replica
// N cannot be placed, decidePlacements returns nothing at all, not the
// replicas it managed to place before failing.
func TestDecidePlacementsFailsAtomicallyNoPartialResult(t *testing.T) {
	node := newNode(2, 2048) // room for exactly one replica of 2 CPU/2048
	d := testDeployment(2, 2, 2048)

	placements, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded scheduling 2 replicas onto capacity for 1, want failure")
	}
	assertWrapsInsufficientCapacity(t, err)
	if placements != nil {
		t.Errorf("decidePlacements returned non-nil placements alongside an error: %+v, want nil", placements)
	}
}

// Test 13 — Idempotency: every replica already placed means nothing new.
func TestDecidePlacementsAlreadyFullyScheduledIsNoOp(t *testing.T) {
	node := newNode(4, 4096)
	d := testDeployment(2, 1, 1024)
	existing := []Placement{
		{ID: uuid.New(), DeploymentID: d.ID, ReplicaIndex: 0, NodeID: node.NodeID, CPU: 1, MemoryBytes: 1024, CreatedAt: fixedNow, UpdatedAt: fixedNow},
		{ID: uuid.New(), DeploymentID: d.ID, ReplicaIndex: 1, NodeID: node.NodeID, CPU: 1, MemoryBytes: 1024, CreatedAt: fixedNow, UpdatedAt: fixedNow},
	}

	got, err := decidePlacements(d, []NodeState{node}, existing, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements on an already-fully-scheduled deployment failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d new placements, want 0 (already fully scheduled)", len(got))
	}
}

// Test 14 — Partial scheduling: existing replicas are never recreated or
// moved; only the missing index is scheduled.
func TestDecidePlacementsPartiallyScheduledOnlyFillsGap(t *testing.T) {
	nodeA := newNode(4, 4096)
	nodeB := newNode(4, 4096)
	d := testDeployment(3, 1, 1024)

	existingID := uuid.New()
	existing := []Placement{
		{ID: existingID, DeploymentID: d.ID, ReplicaIndex: 0, NodeID: nodeA.NodeID, CPU: 1, MemoryBytes: 1024, CreatedAt: fixedNow, UpdatedAt: fixedNow},
		// replica 1 missing
		{ID: uuid.New(), DeploymentID: d.ID, ReplicaIndex: 2, NodeID: nodeB.NodeID, CPU: 1, MemoryBytes: 1024, CreatedAt: fixedNow, UpdatedAt: fixedNow},
	}

	got, err := decidePlacements(d, []NodeState{nodeA, nodeB}, existing, fixedNow)
	if err != nil {
		t.Fatalf("decidePlacements: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d new placements, want exactly 1 (only replica 1 is missing)", len(got))
	}
	if got[0].ReplicaIndex != 1 {
		t.Errorf("new placement's ReplicaIndex = %d, want 1", got[0].ReplicaIndex)
	}
	// The pre-existing placements' IDs must not appear as "new" — decide
	// never re-creates or moves them.
	for _, p := range got {
		if p.ID == existingID {
			t.Error("decidePlacements returned the pre-existing placement's own ID as if newly created")
		}
	}
}

// Overflow, at the algorithm level: a deployment whose per-replica request
// is itself absurd (larger than any node's total capacity) must be
// rejected as ordinary insufficient capacity, never panic or wrap into an
// apparently-valid placement. (Exhaustive int64 boundary arithmetic is
// covered directly in resources_test.go; this is the algorithm-level
// sanity check that a boundary value flows through decidePlacements
// safely.)
func TestDecidePlacementsHandlesNearMaxInt64Request(t *testing.T) {
	node := newNode(1<<62, 1<<62)
	d := testDeployment(1, math.MaxInt64, math.MaxInt64)

	_, err := decidePlacements(d, []NodeState{node}, nil, fixedNow)
	if err == nil {
		t.Fatal("decidePlacements succeeded for a request exceeding the node's capacity, want ErrInsufficientCapacity")
	}
	assertWrapsInsufficientCapacity(t, err)
}

func assertWrapsInsufficientCapacity(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Errorf("error = %v, want it to wrap ErrInsufficientCapacity", err)
	}
}

func placementsEqual(a, b []Placement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ReplicaIndex != b[i].ReplicaIndex || a[i].NodeID != b[i].NodeID ||
			a[i].DeploymentID != b[i].DeploymentID || a[i].CPU != b[i].CPU || a[i].MemoryBytes != b[i].MemoryBytes {
			return false
		}
	}
	return true
}
