package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validInput() RegisterInput {
	return RegisterInput{
		Hostname:            "worker-01",
		OS:                  "linux",
		Architecture:        "amd64",
		CPUCapacity:         8,
		MemoryCapacityBytes: 17179869184,
		AgentVersion:        "0.1.0",
	}
}

func newTestService(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	repo := newFakeRepository()
	svc, err := NewService(repo, time.Second, 3)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo
}

func TestNewServiceRejectsInvalidArguments(t *testing.T) {
	repo := newFakeRepository()

	if _, err := NewService(nil, time.Second, 3); err == nil {
		t.Error("NewService with nil repository: want error, got nil")
	}
	if _, err := NewService(repo, 0, 3); err == nil {
		t.Error("NewService with zero interval: want error, got nil")
	}
	if _, err := NewService(repo, time.Second, 0); err == nil {
		t.Error("NewService with zero threshold: want error, got nil")
	}
}

func TestRegisterNewNodeCreatesReadyNode(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if result.Node.ID == uuid.Nil {
		t.Error("Register did not assign a node ID")
	}
	if result.Node.Status != StatusReady {
		t.Errorf("Status = %q, want %q", result.Node.Status, StatusReady)
	}
	if result.Node.LastHeartbeatAt == nil {
		t.Error("LastHeartbeatAt is nil, want it set at registration")
	}
	if result.HeartbeatInterval != time.Second {
		t.Errorf("HeartbeatInterval = %s, want 1s", result.HeartbeatInterval)
	}
	if repo.upsertCalls != 1 {
		t.Errorf("upsertCalls = %d, want 1", repo.upsertCalls)
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	tests := map[string]func(*RegisterInput){
		"empty hostname":      func(in *RegisterInput) { in.Hostname = "" },
		"empty os":            func(in *RegisterInput) { in.OS = "" },
		"empty architecture":  func(in *RegisterInput) { in.Architecture = "" },
		"empty agent version": func(in *RegisterInput) { in.AgentVersion = "" },
		"zero cpu":            func(in *RegisterInput) { in.CPUCapacity = 0 },
		"negative cpu":        func(in *RegisterInput) { in.CPUCapacity = -1 },
		"zero memory":         func(in *RegisterInput) { in.MemoryCapacityBytes = 0 },
		"negative memory":     func(in *RegisterInput) { in.MemoryCapacityBytes = -1 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			in := validInput()
			mutate(&in)

			_, err := svc.Register(context.Background(), in)
			if err == nil {
				t.Fatal("Register succeeded, want a validation error")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Errorf("error = %v, want a *ValidationError", err)
			}
			if repo.upsertCalls != 0 {
				t.Errorf("upsertCalls = %d, want 0 — invalid input must never reach the repository", repo.upsertCalls)
			}
		})
	}
}

func TestRegisterReportsEveryProblemAtOnce(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Register(context.Background(), RegisterInput{})
	if err == nil {
		t.Fatal("Register succeeded, want an error")
	}
	for _, want := range []string{"hostname", "os", "architecture", "agent_version", "cpu_capacity", "memory_capacity_bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestRegisterExistingNodeIDIsIdempotent(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	first, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	in := validInput()
	in.NodeID = first.Node.ID
	in.Hostname = "worker-01-renamed"

	second, err := svc.Register(ctx, in)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}

	if second.Node.ID != first.Node.ID {
		t.Errorf("node ID changed across re-registration: %s -> %s", first.Node.ID, second.Node.ID)
	}
	if second.Node.Hostname != "worker-01-renamed" {
		t.Errorf("Hostname = %q, want updated value", second.Node.Hostname)
	}
	if second.Node.RegisteredAt != first.Node.RegisteredAt {
		t.Error("RegisteredAt changed across re-registration, want it preserved")
	}
	if len(repo.nodes) != 1 {
		t.Errorf("repository has %d nodes, want exactly 1 (no duplicate row)", len(repo.nodes))
	}
	if repo.upsertCalls != 2 {
		t.Errorf("upsertCalls = %d, want 2", repo.upsertCalls)
	}
}

func TestRegisterWithoutNodeIDAlwaysCreatesANewNode(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	first, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	second, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}

	if first.Node.ID == second.Node.ID {
		t.Error("two registrations without a node_id produced the same ID")
	}
	if len(repo.nodes) != 2 {
		t.Errorf("repository has %d nodes, want 2", len(repo.nodes))
	}
}

func TestHeartbeatUpdatesLastHeartbeatAt(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	later := fixed.Add(5 * time.Second)
	svc.now = func() time.Time { return later }

	hb, err := svc.Heartbeat(ctx, result.Node.ID)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if hb.Node.LastHeartbeatAt == nil || !hb.Node.LastHeartbeatAt.Equal(later) {
		t.Errorf("LastHeartbeatAt = %v, want %v", hb.Node.LastHeartbeatAt, later)
	}
	if hb.Node.Status != StatusReady {
		t.Errorf("Status = %q, want %q", hb.Node.Status, StatusReady)
	}
	if hb.Recovered {
		t.Error("Recovered = true, want false — node was already Ready")
	}
}

func TestHeartbeatForUnknownNodeReturnsErrNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Heartbeat(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestHeartbeatRecoversNotReadyNodeToReady(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Force the node into NotReady directly, as the membership monitor would.
	repo.mu.Lock()
	node := repo.nodes[result.Node.ID]
	node.Status = StatusNotReady
	repo.nodes[result.Node.ID] = node
	repo.mu.Unlock()

	hb, err := svc.Heartbeat(ctx, result.Node.ID)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if hb.Node.Status != StatusReady {
		t.Errorf("Status = %q, want %q after a heartbeat from a NotReady node", hb.Node.Status, StatusReady)
	}
	if !hb.Recovered {
		t.Error("Recovered = false, want true — node was NotReady immediately before this heartbeat")
	}
}

func TestGetUnknownNodeReturnsErrNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestListOrdersByHostname(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for _, hostname := range []string{"charlie", "alice", "bob"} {
		in := validInput()
		in.Hostname = hostname
		if _, err := svc.Register(ctx, in); err != nil {
			t.Fatalf("Register(%s): %v", hostname, err)
		}
	}

	nodes, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("List returned %d nodes, want 3", len(nodes))
	}
	got := []string{nodes[0].Hostname, nodes[1].Hostname, nodes[2].Hostname}
	want := []string{"alice", "bob", "charlie"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nodes[%d].Hostname = %q, want %q (got order %v)", i, got[i], want[i], got)
		}
	}
}

// --- Failure detection ---------------------------------------------------

func TestDetectStaleNodesLeavesFreshNodesReady(t *testing.T) {
	svc, _ := newTestService(t) // interval=1s, threshold=3 => timeout=3s
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	stale, err := svc.DetectStaleNodes(ctx, start.Add(1*time.Second))
	if err != nil {
		t.Fatalf("DetectStaleNodes: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("DetectStaleNodes returned %d stale nodes, want 0 (well within timeout)", len(stale))
	}

	node, err := svc.Get(ctx, result.Node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if node.Status != StatusReady {
		t.Errorf("Status = %q, want %q", node.Status, StatusReady)
	}
}

func TestDetectStaleNodesLeavesNodesWithinThresholdReady(t *testing.T) {
	svc, _ := newTestService(t) // timeout=3s
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Exactly at the timeout boundary: "later - lastHeartbeat == timeout"
	// must not yet count as stale (the repository uses a strict "<" cutoff).
	stale, err := svc.DetectStaleNodes(ctx, start.Add(svc.HeartbeatTimeout()))
	if err != nil {
		t.Fatalf("DetectStaleNodes: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("DetectStaleNodes returned %d stale nodes at the exact timeout boundary, want 0", len(stale))
	}

	node, _ := svc.Get(ctx, result.Node.ID)
	if node.Status != StatusReady {
		t.Errorf("Status = %q, want %q", node.Status, StatusReady)
	}
}

func TestDetectStaleNodesMarksNodesBeyondThresholdNotReady(t *testing.T) {
	svc, _ := newTestService(t) // timeout=3s
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	past := start.Add(svc.HeartbeatTimeout() + time.Millisecond)
	stale, err := svc.DetectStaleNodes(ctx, past)
	if err != nil {
		t.Fatalf("DetectStaleNodes: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("DetectStaleNodes returned %d stale nodes, want 1", len(stale))
	}
	if stale[0].ID != result.Node.ID {
		t.Errorf("stale node ID = %s, want %s", stale[0].ID, result.Node.ID)
	}
	if stale[0].Status != StatusNotReady {
		t.Errorf("Status = %q, want %q", stale[0].Status, StatusNotReady)
	}

	node, _ := svc.Get(ctx, result.Node.ID)
	if node.Status != StatusNotReady {
		t.Errorf("persisted Status = %q, want %q", node.Status, StatusNotReady)
	}
}

func TestDetectStaleNodesDoesNotRepeatAlreadyStaleNodes(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	if _, err := svc.Register(ctx, validInput()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	past := start.Add(svc.HeartbeatTimeout() + time.Second)
	first, err := svc.DetectStaleNodes(ctx, past)
	if err != nil || len(first) != 1 {
		t.Fatalf("first sweep: nodes=%d err=%v, want 1 node, no error", len(first), err)
	}

	// A node already NotReady must not be reported again on the next sweep —
	// only transitions are reported, so logs never spam for unchanged nodes.
	second, err := svc.DetectStaleNodes(ctx, past.Add(time.Second))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second sweep returned %d nodes, want 0 (already NotReady, no new transition)", len(second))
	}
}

func TestHeartbeatAfterFailureDetectionRecoversNode(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	past := start.Add(svc.HeartbeatTimeout() + time.Second)
	if _, err := svc.DetectStaleNodes(ctx, past); err != nil {
		t.Fatalf("DetectStaleNodes: %v", err)
	}
	if node, _ := svc.Get(ctx, result.Node.ID); node.Status != StatusNotReady {
		t.Fatalf("precondition failed: node is %q, want %q", node.Status, StatusNotReady)
	}

	svc.now = func() time.Time { return past.Add(time.Second) }
	hb, err := svc.Heartbeat(ctx, result.Node.ID)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if hb.Node.Status != StatusReady {
		t.Errorf("Status = %q, want %q after recovery heartbeat", hb.Node.Status, StatusReady)
	}
	if !hb.Recovered {
		t.Error("Recovered = false, want true")
	}
}

// --- Concurrency -----------------------------------------------------------

func TestConcurrentHeartbeatsAreSafe(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	result, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Heartbeat(ctx, result.Node.ID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Heartbeat: %v", err)
	}

	node, err := svc.Get(ctx, result.Node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if node.Status != StatusReady {
		t.Errorf("Status = %q, want %q", node.Status, StatusReady)
	}
}

func TestConcurrentRegistrationsOfTheSameNodeIDConverge(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()
	nodeID := uuid.New()

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := validInput()
			in.NodeID = nodeID
			_, _ = svc.Register(ctx, in)
		}()
	}
	wg.Wait()

	if len(repo.nodes) != 1 {
		t.Errorf("repository has %d nodes after %d concurrent registrations of the same ID, want 1", len(repo.nodes), n)
	}
}
