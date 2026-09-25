package scheduler

import (
	"fmt"
	"math"

	"github.com/google/uuid"
)

// NodeState is a Ready node's resource capacity together with what is
// already allocated to it, cluster-wide, at the moment scheduling runs. It
// is the scheduler's own working representation of a node — deliberately
// not cluster.Node — since scheduling needs exactly these four numbers plus
// an identity, nothing more.
//
// AllocatedCPU and AllocatedMemory must already reflect every existing
// placement on this node across every deployment, not just the deployment
// currently being scheduled — see decide's doc comment and
// docs/scheduling.md's "Resource accounting" section for why cluster-wide
// accounting, rather than per-deployment accounting, is the only correct
// choice here.
type NodeState struct {
	NodeID          uuid.UUID
	CPUCapacity     int64
	MemoryCapacity  int64
	AllocatedCPU    int64
	AllocatedMemory int64
}

// AvailableCPU is this node's unreserved CPU. It never reports negative —
// capacity is validated positive at node-registration time and allocation
// only ever grows through this package's own overflow-checked Reserve, so
// AllocatedCPU should never legitimately exceed CPUCapacity, but clamping
// here costs nothing and removes any possibility of a negative "available"
// figure leaking into a scheduling decision. The subtraction itself is
// overflow-checked too (via subOverflowSafe): plain int64 subtraction wraps
// silently on overflow rather than panicking, which for a corrupted or
// otherwise unexpected AllocatedCPU value (never produced by this
// package's own code, but not something this method assumes) could
// otherwise wrap around to a large *positive* number and be mistaken for
// real headroom — treating that case as "no room" (0) is the only safe
// default.
func (n NodeState) AvailableCPU() int64 {
	avail, ok := subOverflowSafe(n.CPUCapacity, n.AllocatedCPU)
	if !ok || avail < 0 {
		return 0
	}
	return avail
}

// AvailableMemory is this node's unreserved memory, in bytes. See
// AvailableCPU for why it never reports negative and why the subtraction
// itself is overflow-checked.
func (n NodeState) AvailableMemory() int64 {
	avail, ok := subOverflowSafe(n.MemoryCapacity, n.AllocatedMemory)
	if !ok || avail < 0 {
		return 0
	}
	return avail
}

// CanFit reports whether this node currently has enough unreserved CPU AND
// memory for a replica requesting cpu/memoryBytes. Both resources must fit
// — a node with abundant memory but no CPU headroom is not a candidate,
// and vice versa.
func (n NodeState) CanFit(cpu, memoryBytes int64) bool {
	if cpu <= 0 || memoryBytes <= 0 {
		// A request for zero or negative resources is never legitimate —
		// deployment.CreateInput.Validate already rejects this at creation
		// time, so a well-formed caller never reaches this branch, but a
		// scheduler must never report "fits" for a nonsensical request.
		return false
	}
	return n.AvailableCPU() >= cpu && n.AvailableMemory() >= memoryBytes
}

// Reserve returns a new NodeState with cpu/memoryBytes added to this node's
// allocation. It fails if the node cannot currently fit the request (the
// caller is expected to have already checked CanFit, but Reserve
// re-verifies rather than trusting that), or if adding would overflow
// int64 — which CanFit's own capacity-bounded check makes unreachable in
// practice, but Reserve never assumes that and always guards the
// arithmetic itself.
//
// Reserve does not mutate n: it returns an updated copy. The scheduling
// algorithm threads this returned value through its own working state
// explicitly (see decide in scheduler.go) rather than relying on any
// shared mutable NodeState, so each candidate node's state after
// placing one replica is exactly and only what that algorithm computed.
func (n NodeState) Reserve(cpu, memoryBytes int64) (NodeState, error) {
	if !n.CanFit(cpu, memoryBytes) {
		return NodeState{}, fmt.Errorf("scheduler: node %s cannot fit %d CPU / %d bytes memory (available: %d CPU / %d bytes)",
			n.NodeID, cpu, memoryBytes, n.AvailableCPU(), n.AvailableMemory())
	}

	newCPU, ok := addOverflowSafe(n.AllocatedCPU, cpu)
	if !ok {
		return NodeState{}, fmt.Errorf("%w: reserving %d CPU on node %s", ErrResourceOverflow, cpu, n.NodeID)
	}
	newMemory, ok := addOverflowSafe(n.AllocatedMemory, memoryBytes)
	if !ok {
		return NodeState{}, fmt.Errorf("%w: reserving %d bytes memory on node %s", ErrResourceOverflow, memoryBytes, n.NodeID)
	}

	n.AllocatedCPU = newCPU
	n.AllocatedMemory = newMemory
	return n, nil
}

// addOverflowSafe adds a and b, reporting ok=false instead of silently
// wrapping if the result would not fit in an int64. Both operands are
// always non-negative in this package's own call sites (capacities and
// allocations are never negative — see CHECK constraints in
// 0001/0002/0003), but the check itself is written generally rather than
// assuming that, so it stays correct even if a future caller passes a
// value this package did not anticipate.
func addOverflowSafe(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

// subOverflowSafe subtracts b from a, reporting ok=false instead of
// silently wrapping if the result would not fit in an int64. Deliberately
// not implemented as addOverflowSafe(a, -b): negating b overflows in its
// own right when b is math.MinInt64, so this checks the subtraction's two
// overflow directions directly instead.
func subOverflowSafe(a, b int64) (int64, bool) {
	if b > 0 && a < math.MinInt64+b {
		return 0, false
	}
	if b < 0 && a > math.MaxInt64+b {
		return 0, false
	}
	return a - b, true
}
