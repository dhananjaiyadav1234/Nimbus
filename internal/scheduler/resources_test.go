package scheduler

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestNodeStateAvailableNeverNegative(t *testing.T) {
	n := NodeState{NodeID: uuid.New(), CPUCapacity: 4, MemoryCapacity: 4096, AllocatedCPU: 10, AllocatedMemory: 999999}
	if got := n.AvailableCPU(); got != 0 {
		t.Errorf("AvailableCPU() = %d, want 0 (over-allocated should clamp, never go negative)", got)
	}
	if got := n.AvailableMemory(); got != 0 {
		t.Errorf("AvailableMemory() = %d, want 0", got)
	}
}

func TestNodeStateCanFit(t *testing.T) {
	n := NodeState{NodeID: uuid.New(), CPUCapacity: 4, MemoryCapacity: 4096, AllocatedCPU: 1, AllocatedMemory: 1024}
	// Available: 3 CPU, 3072 memory.

	tests := []struct {
		name        string
		cpu, memory int64
		want        bool
	}{
		{"fits comfortably", 1, 1024, true},
		{"exact fit", 3, 3072, true},
		{"cpu insufficient", 4, 1024, false},
		{"memory insufficient", 1, 4096, false},
		{"both insufficient", 10, 10000, false},
		{"zero cpu request rejected", 0, 1024, false},
		{"zero memory request rejected", 1, 0, false},
		{"negative cpu request rejected", -1, 1024, false},
		{"negative memory request rejected", 1, -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := n.CanFit(tt.cpu, tt.memory); got != tt.want {
				t.Errorf("CanFit(%d, %d) = %v, want %v", tt.cpu, tt.memory, got, tt.want)
			}
		})
	}
}

func TestNodeStateReserveUpdatesAllocation(t *testing.T) {
	n := NodeState{NodeID: uuid.New(), CPUCapacity: 4, MemoryCapacity: 4096, AllocatedCPU: 0, AllocatedMemory: 0}

	reserved, err := n.Reserve(1, 1024)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if reserved.AllocatedCPU != 1 || reserved.AllocatedMemory != 1024 {
		t.Errorf("after Reserve: AllocatedCPU=%d AllocatedMemory=%d, want 1/1024", reserved.AllocatedCPU, reserved.AllocatedMemory)
	}
	// Original value must be untouched — Reserve returns a copy, it does
	// not mutate the receiver.
	if n.AllocatedCPU != 0 || n.AllocatedMemory != 0 {
		t.Errorf("original NodeState was mutated: AllocatedCPU=%d AllocatedMemory=%d, want 0/0", n.AllocatedCPU, n.AllocatedMemory)
	}
}

func TestNodeStateReserveRejectsWhatDoesNotFit(t *testing.T) {
	n := NodeState{NodeID: uuid.New(), CPUCapacity: 1, MemoryCapacity: 1024, AllocatedCPU: 0, AllocatedMemory: 0}
	if _, err := n.Reserve(2, 1024); err == nil {
		t.Fatal("Reserve succeeded for a request exceeding capacity, want an error")
	}
}

func TestNodeStateReserveChaining(t *testing.T) {
	// Simulates the scheduler's own working-state loop: each Reserve call
	// must account for every previous one.
	n := NodeState{NodeID: uuid.New(), CPUCapacity: 4, MemoryCapacity: 4096, AllocatedCPU: 0, AllocatedMemory: 0}

	n, err := n.Reserve(2, 2048)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if !n.CanFit(2, 2048) {
		t.Fatal("after reserving 2/2048 of a 4/4096 node, want it to still fit exactly one more 2/2048 request")
	}
	n, err = n.Reserve(2, 2048)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if n.CanFit(1, 1) {
		t.Errorf("node fully reserved (4/4 CPU, 4096/4096 memory) still reports CanFit(1,1), want false")
	}
}

func TestAddOverflowSafe(t *testing.T) {
	tests := []struct {
		name    string
		a, b    int64
		wantOK  bool
		wantSum int64
	}{
		{"ordinary addition", 3, 4, true, 7},
		{"zero plus zero", 0, 0, true, 0},
		{"at MaxInt64 boundary, adding zero", math.MaxInt64, 0, true, math.MaxInt64},
		{"one below MaxInt64 plus one", math.MaxInt64 - 1, 1, true, math.MaxInt64},
		{"overflow: MaxInt64 plus one", math.MaxInt64, 1, false, 0},
		{"overflow: MaxInt64 plus MaxInt64", math.MaxInt64, math.MaxInt64, false, 0},
		{"at MinInt64 boundary, adding zero", math.MinInt64, 0, true, math.MinInt64},
		{"underflow: MinInt64 plus negative one", math.MinInt64, -1, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum, ok := addOverflowSafe(tt.a, tt.b)
			if ok != tt.wantOK {
				t.Fatalf("addOverflowSafe(%d, %d) ok = %v, want %v", tt.a, tt.b, ok, tt.wantOK)
			}
			if ok && sum != tt.wantSum {
				t.Errorf("addOverflowSafe(%d, %d) = %d, want %d", tt.a, tt.b, sum, tt.wantSum)
			}
		})
	}
}

func TestNodeStateReserveReportsOverflowInsteadOfWrapping(t *testing.T) {
	// A node whose AllocatedCPU is already at the edge of int64 (this can
	// never happen through the normal API — cluster.RegisterInput.Validate
	// and deployment.CreateInput.Validate both bound their inputs far below
	// this — but Reserve must never silently produce a wrapped, apparently
	// valid negative allocation if it somehow did).
	n := NodeState{
		NodeID:          uuid.New(),
		CPUCapacity:     math.MaxInt64,
		MemoryCapacity:  math.MaxInt64,
		AllocatedCPU:    math.MaxInt64 - 1,
		AllocatedMemory: 0,
	}
	// CanFit(2, ...) is false here (only 1 available), which is itself the
	// first line of defense — Reserve must refuse rather than overflow.
	if n.CanFit(2, 1) {
		t.Fatal("CanFit reported true for a request that would overflow allocation, want false")
	}
	if _, err := n.Reserve(2, 1); err == nil {
		t.Fatal("Reserve succeeded for a request that would overflow allocation, want an error")
	}
}

func TestSubOverflowSafe(t *testing.T) {
	tests := []struct {
		name    string
		a, b    int64
		wantOK  bool
		wantDif int64
	}{
		{"ordinary subtraction", 10, 4, true, 6},
		{"result exactly zero", 5, 5, true, 0},
		{"result negative but representable", 3, 10, true, -7},
		{"MaxInt64 minus zero", math.MaxInt64, 0, true, math.MaxInt64},
		{"MinInt64 minus zero", math.MinInt64, 0, true, math.MinInt64},
		{"MinInt64 minus one: underflow", math.MinInt64, 1, false, 0},
		{"MaxInt64 minus negative one: overflow", math.MaxInt64, -1, false, 0},
		{"zero minus MinInt64: overflow (negating MinInt64 alone overflows)", 0, math.MinInt64, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, ok := subOverflowSafe(tt.a, tt.b)
			if ok != tt.wantOK {
				t.Fatalf("subOverflowSafe(%d, %d) ok = %v, want %v", tt.a, tt.b, ok, tt.wantOK)
			}
			if ok && diff != tt.wantDif {
				t.Errorf("subOverflowSafe(%d, %d) = %d, want %d", tt.a, tt.b, diff, tt.wantDif)
			}
		})
	}
}

func TestNodeStateAvailableCPUSafeUnderCorruptedNegativeAllocation(t *testing.T) {
	// AllocatedCPU should never legitimately be negative — every value
	// this package writes comes from either 0 or a prior overflow-checked
	// Reserve — but AvailableCPU must never turn a corrupted negative
	// allocation into a wrapped, apparently-huge-and-valid available
	// figure. Simulates that corrupted state directly, bypassing Reserve.
	n := NodeState{NodeID: uuid.New(), CPUCapacity: math.MaxInt64, AllocatedCPU: math.MinInt64}
	if got := n.AvailableCPU(); got != 0 {
		t.Errorf("AvailableCPU() with corrupted negative allocation = %d, want 0 (safe default, not a wrapped value)", got)
	}
}
