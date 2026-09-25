// Package scheduler owns Nimbus's Phase 2.2 scheduling domain: given a
// Deployment's desired replica count and resource request, and the
// cluster's current Ready-node capacity and existing placements, it decides
// which node each not-yet-placed replica should be assigned to, and
// persists that decision.
//
// This package answers exactly one question — "where should each replica
// go?" — and nothing past it. It does not create, start, stop, or inspect
// any container; it does not know internal/runtime or Docker exist at all.
// It does not reconcile a failed node's placements onto a new one, restart
// anything, or watch cluster state in the background. Those are later
// phases' concerns. See docs/scheduling.md for the full phase boundary.
package scheduler

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
)

// Placement is a single decided (and, once returned from Repository, always
// persisted) assignment: replica ReplicaIndex of deployment DeploymentID has
// been reserved onto node NodeID, consuming CPU/MemoryBytes of that node's
// capacity. It is the row shape of the `deployment_placements` table plus
// nothing else — handlers translate it to and from wire formats; the type
// itself carries no HTTP or JSON concerns, mirroring deployment.Deployment
// and cluster.Node.
type Placement struct {
	ID           uuid.UUID
	DeploymentID uuid.UUID
	ReplicaIndex int
	NodeID       uuid.UUID
	CPU          int64
	MemoryBytes  int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DecideFunc computes the new placements needed to bring deploymentID's
// existing placements up to its desired replica count, given nodes — the
// Ready nodes' capacity and *cluster-wide* current allocation, as read and
// row-locked by Repository.Schedule immediately before calling this — and
// existing — the placements deploymentID already has.
//
// A DecideFunc must be pure: no I/O, no SQL, no goroutines, no randomness,
// and no dependence on anything but its arguments — Repository.Schedule
// calls it once, synchronously, from inside an open database transaction,
// and persists exactly what it returns. See decidePlacements for Nimbus's
// own implementation and the algorithm it documents.
type DecideFunc func(nodes []NodeState, existing []Placement) ([]Placement, error)

// newDecideFunc closes over the deployment being scheduled and the instant
// scheduling was invoked, producing a DecideFunc that implements Nimbus's
// deterministic best-fit algorithm — see decidePlacements.
func newDecideFunc(d deployment.Deployment, now time.Time) DecideFunc {
	return func(nodes []NodeState, existing []Placement) ([]Placement, error) {
		return decidePlacements(d, nodes, existing, now)
	}
}

// decidePlacements is Nimbus's scheduling algorithm: deterministic
// best-fit / least-waste, resource-aware, with a stable ascending-node-ID
// tie-break. See docs/scheduling.md's "Scheduling algorithm" section for
// the full documented rationale; this comment covers the mechanics.
//
// Only replica indexes not already present in existing are placed — this
// is what makes scheduling both idempotent (nothing to do once every
// replica already has a placement) and safe to call on a partially
// scheduled deployment (existing replicas are never moved or recreated;
// see docs/scheduling.md's "Idempotency" and "Multiple replicas" sections).
//
// The whole plan is computed against one working copy of node state,
// updated after each replica is placed (Reserve), so replica N's placement
// decision correctly accounts for replicas 0..N-1's reservations from this
// same scheduling attempt — not just what was already persisted before it
// started. If any replica cannot be placed, the entire call fails with
// ErrInsufficientCapacity and returns no placements at all: Repository.Schedule
// persists nothing when this happens, so a partially-computed plan can
// never reach the database (atomicity — see docs/scheduling.md).
func decidePlacements(d deployment.Deployment, nodes []NodeState, existing []Placement, now time.Time) ([]Placement, error) {
	placedReplica := make(map[int]bool, len(existing))
	for _, p := range existing {
		placedReplica[p.ReplicaIndex] = true
	}

	var missing []int
	for i := 0; i < d.Replicas; i++ {
		if !placedReplica[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		// Every replica already has a placement — this call has nothing new
		// to do. Returning an empty, non-nil slice (rather than an error)
		// is what makes repeated scheduling of an already-fully-scheduled
		// deployment a successful no-op instead of a failure.
		return []Placement{}, nil
	}

	// A fixed, ascending-by-ID starting order is the deterministic base
	// every tie-break in this function relies on — see selectBestFit. Sort
	// once, up front, rather than trusting the order Repository.Schedule's
	// SQL happened to return (that order is itself already
	// deterministic — ORDER BY id — but decidePlacements does not trust
	// its caller for this; it establishes its own deterministic order
	// unconditionally).
	working := make([]NodeState, len(nodes))
	copy(working, nodes)
	sort.Slice(working, func(i, j int) bool {
		return working[i].NodeID.String() < working[j].NodeID.String()
	})

	placements := make([]Placement, 0, len(missing))
	for _, replicaIndex := range missing {
		bestIdx, ok := selectBestFit(working, d.CPU, d.MemoryBytes)
		if !ok {
			return nil, fmt.Errorf("%w: replica %d of deployment %q needs %d CPU / %d bytes memory, no Ready node has that much available",
				ErrInsufficientCapacity, replicaIndex, d.Name, d.CPU, d.MemoryBytes)
		}

		reserved, err := working[bestIdx].Reserve(d.CPU, d.MemoryBytes)
		if err != nil {
			// Unreachable in practice: selectBestFit only returns indexes
			// that already passed CanFit, and CanFit is exactly what
			// Reserve re-checks first. Guarded anyway — see Reserve's own
			// doc comment for why it never trusts a caller's prior check.
			return nil, fmt.Errorf("scheduler: %w", err)
		}
		working[bestIdx] = reserved

		placements = append(placements, Placement{
			ID:           uuid.New(),
			DeploymentID: d.ID,
			ReplicaIndex: replicaIndex,
			NodeID:       working[bestIdx].NodeID,
			CPU:          d.CPU,
			MemoryBytes:  d.MemoryBytes,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}

	return placements, nil
}

// selectBestFit returns the index into nodes of the best candidate for a
// replica requesting cpu/memoryBytes, or ok=false if no node currently has
// room. "Best" is the node that would be left with the least proportional
// headroom after placing this replica (least-waste / tightest-fit),
// expressed as the sum of its remaining-CPU and remaining-memory ratios to
// that node's own total capacity — see fitScore.
//
// nodes must already be sorted by NodeID ascending (decidePlacements'
// caller-established order) — that fixed order, combined with
// sort.SliceStable below, is what gives equal-score candidates a
// deterministic ascending-node-ID tie-break without ever comparing UUIDs
// directly in the sort itself: a stable sort never reorders equal
// elements, so two candidates with identical scores stay in the relative
// order they were considered in, which is nodes' own ID-ascending order.
func selectBestFit(nodes []NodeState, cpu, memoryBytes int64) (int, bool) {
	type candidate struct {
		index int
		score float64
	}

	candidates := make([]candidate, 0, len(nodes))
	for i, n := range nodes {
		if !n.CanFit(cpu, memoryBytes) {
			continue
		}
		candidates = append(candidates, candidate{index: i, score: fitScore(n, cpu, memoryBytes)})
	}
	if len(candidates) == 0 {
		return 0, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score < candidates[j].score
	})
	return candidates[0].index, true
}

// fitScore is lower for a tighter fit. remainingCPU/remainingMemory are
// what node n would have left, after placing this replica, expressed as a
// fraction of the node's own total capacity so that nodes of very
// different sizes are compared fairly (a node with 1 CPU free out of 2 is
// "tighter" than one with 1 CPU free out of 64, even though the raw
// available-CPU numbers alone would not show that). The two ratios are
// summed with equal weight — CPU and memory pressure are treated as
// equally important, since Phase 2.2 has no basis yet to prefer one over
// the other. See docs/scheduling.md for the fully worked example this
// scoring model is documented against.
func fitScore(n NodeState, cpu, memoryBytes int64) float64 {
	remainingCPU := n.AvailableCPU() - cpu
	remainingMemory := n.AvailableMemory() - memoryBytes

	cpuRatio := float64(remainingCPU) / float64(n.CPUCapacity)
	memoryRatio := float64(remainingMemory) / float64(n.MemoryCapacity)
	return cpuRatio + memoryRatio
}
