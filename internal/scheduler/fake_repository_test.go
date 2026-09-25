package scheduler

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
)

// fakeRepository is an in-memory Repository used by unit tests, mirroring
// internal/cluster's and internal/deployment's fakeRepository pattern.
//
// Schedule below reproduces Repository.Schedule's observable contract
// (lock-equivalent read-then-write consistency, atomicity on a decide
// error, idempotent no-op when nothing is missing) using a plain mutex
// rather than PostgreSQL row locks — a real mutex is an acceptable stand-in
// for a *unit* test double exactly because the actual cross-transaction
// concurrency guarantee is proved separately, against a real database,
// by TestPostgresRepositoryConcurrentScheduleIsRaceSafe in
// repository_test.go. That distinction — fake enforces the contract
// in-process for algorithm tests, Postgres proves the concurrency
// guarantee for real — mirrors exactly how
// internal/deployment/fake_repository_test.go's own comment describes its
// application-level uniqueness check.
type fakeRepository struct {
	mu         sync.Mutex
	nodes      map[uuid.UUID]NodeState
	deployment map[uuid.UUID]bool // set of "known" deployment IDs
	placements map[uuid.UUID][]Placement
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		nodes:      map[uuid.UUID]NodeState{},
		deployment: map[uuid.UUID]bool{},
		placements: map[uuid.UUID][]Placement{},
	}
}

// addNode registers a Ready node's capacity with the fake, as if it were
// already locked and read from the nodes table.
func (f *fakeRepository) addNode(n NodeState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[n.NodeID] = n
}

// knowDeployment marks id as an existing deployment row, so Schedule does
// not report ErrDeploymentNotFound for it.
func (f *fakeRepository) knowDeployment(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployment[id] = true
}

func (f *fakeRepository) ListByDeployment(_ context.Context, deploymentID uuid.UUID) ([]Placement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	existing := append([]Placement{}, f.placements[deploymentID]...)
	sort.Slice(existing, func(i, j int) bool { return existing[i].ReplicaIndex < existing[j].ReplicaIndex })
	return existing, nil
}

func (f *fakeRepository) Schedule(_ context.Context, deploymentID uuid.UUID, decide DecideFunc) ([]Placement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.deployment[deploymentID] {
		return nil, ErrDeploymentNotFound
	}

	// Cluster-wide allocation across every deployment's placements,
	// exactly like clusterWideAllocation in repository.go.
	allocated := map[uuid.UUID]struct{ cpu, memory int64 }{}
	for _, placements := range f.placements {
		for _, p := range placements {
			a := allocated[p.NodeID]
			a.cpu += p.CPU
			a.memory += p.MemoryBytes
			allocated[p.NodeID] = a
		}
	}

	nodes := make([]NodeState, 0, len(f.nodes))
	for id, n := range f.nodes {
		a := allocated[id]
		n.AllocatedCPU = a.cpu
		n.AllocatedMemory = a.memory
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID.String() < nodes[j].NodeID.String() })

	existing := append([]Placement{}, f.placements[deploymentID]...)
	sort.Slice(existing, func(i, j int) bool { return existing[i].ReplicaIndex < existing[j].ReplicaIndex })

	newPlacements, err := decide(nodes, existing)
	if err != nil {
		// Atomicity: nothing written to f.placements.
		return nil, err
	}

	if len(newPlacements) > 0 {
		f.placements[deploymentID] = append(f.placements[deploymentID], newPlacements...)
	}

	result := append(append([]Placement{}, existing...), newPlacements...)
	sort.Slice(result, func(i, j int) bool { return result[i].ReplicaIndex < result[j].ReplicaIndex })
	return result, nil
}
