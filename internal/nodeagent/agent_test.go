package nodeagent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
)

// fakeControlPlaneClient is a ControlPlaneClient test double. Each method
// has an overridable function field; a nil field succeeds trivially. Calls
// are counted so tests can assert retry/re-registration behaviour without
// timing assertions.
type fakeControlPlaneClient struct {
	mu sync.Mutex

	registerFunc  func(ctx context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error)
	heartbeatFunc func(ctx context.Context, id uuid.UUID, req clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error)

	registerCalls  int32
	heartbeatCalls int32
}

func (f *fakeControlPlaneClient) Register(ctx context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error) {
	atomic.AddInt32(&f.registerCalls, 1)
	f.mu.Lock()
	fn := f.registerFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	// HeartbeatIntervalSeconds is deliberately 0 (= "no override") rather
	// than some real value: Agent adopts the Control Plane's reported
	// interval verbatim, and the wire contract only has whole-second
	// granularity, which would force every test using the default response
	// onto a multi-second ticker. Leaving it unset keeps the fast interval
	// from testAgentConfig in effect.
	return clusterapi.RegisterResponse{NodeID: req.NodeID, Status: "Ready"}, nil
}

func (f *fakeControlPlaneClient) Heartbeat(ctx context.Context, id uuid.UUID, req clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error) {
	atomic.AddInt32(&f.heartbeatCalls, 1)
	f.mu.Lock()
	fn := f.heartbeatFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, req)
	}
	return clusterapi.HeartbeatResponse{Status: "Ready"}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testAgentConfig(t *testing.T) config.NodeAgentConfig {
	t.Helper()
	return config.NodeAgentConfig{
		ControlPlaneURL:   "http://unused.invalid",
		NodeDataDir:       t.TempDir(),
		NodeName:          "test-node",
		HeartbeatInterval: 5 * time.Millisecond,
		AgentVersion:      "0.1.0-test",
	}
}

// newFastTestAgent builds an Agent whose retry backoff is milliseconds, not
// seconds, and whose machine info is a fixed value — so tests exercise real
// retry/heartbeat control flow without waiting on real timers or depending
// on the host machine's actual hardware.
func newFastTestAgent(t *testing.T, client ControlPlaneClient) *Agent {
	t.Helper()
	a := New(testAgentConfig(t), testLogger(), client)
	a.registrationBackoffBase = time.Millisecond
	a.registrationBackoffMax = 5 * time.Millisecond
	a.machineInfo = func(nameOverride string) (MachineInfo, error) {
		return MachineInfo{Hostname: nameOverride, OS: "linux", Architecture: "amd64", CPUCapacity: 4, MemoryCapacityBytes: 1 << 30}, nil
	}
	return a
}

func TestAgentRunRegistersOnce(t *testing.T) {
	client := &fakeControlPlaneClient{}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := agent.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if calls := atomic.LoadInt32(&client.registerCalls); calls != 1 {
		t.Errorf("registerCalls = %d, want 1", calls)
	}
}

func TestAgentRunSendsHeartbeats(t *testing.T) {
	client := &fakeControlPlaneClient{}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := agent.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if calls := atomic.LoadInt32(&client.heartbeatCalls); calls == 0 {
		t.Error("heartbeatCalls = 0, want at least one heartbeat sent")
	}
}

func TestAgentIdentityIsStableAcrossRuns(t *testing.T) {
	client := &fakeControlPlaneClient{}
	dataDir := t.TempDir()

	var firstID, secondID string
	client.registerFunc = func(_ context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error) {
		if firstID == "" {
			firstID = req.NodeID
		} else {
			secondID = req.NodeID
		}
		return clusterapi.RegisterResponse{NodeID: req.NodeID, Status: "Ready", HeartbeatIntervalSeconds: 1}, nil
	}

	cfg := testAgentConfig(t)
	cfg.NodeDataDir = dataDir

	run := func() {
		a := New(cfg, testLogger(), client)
		a.registrationBackoffBase = time.Millisecond
		a.registrationBackoffMax = 5 * time.Millisecond
		a.machineInfo = func(nameOverride string) (MachineInfo, error) {
			return MachineInfo{Hostname: nameOverride, OS: "linux", Architecture: "amd64", CPUCapacity: 4, MemoryCapacityBytes: 1 << 30}, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_ = a.Run(ctx)
	}

	run() // first "process" - creates identity
	run() // simulated restart - must reuse it

	if firstID == "" || secondID == "" {
		t.Fatal("registration was not observed twice")
	}
	if firstID != secondID {
		t.Errorf("node ID changed across simulated restart: %s -> %s", firstID, secondID)
	}
}

func TestAgentRetriesRegistrationOnTransientFailure(t *testing.T) {
	var attempts int32
	client := &fakeControlPlaneClient{
		registerFunc: func(_ context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error) {
			if atomic.AddInt32(&attempts, 1) < 3 {
				return clusterapi.RegisterResponse{}, errors.New("connection refused")
			}
			return clusterapi.RegisterResponse{NodeID: req.NodeID, Status: "Ready", HeartbeatIntervalSeconds: 1}, nil
		},
	}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := agent.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("registration attempts = %d, want 3 (2 failures then success)", got)
	}
}

func TestAgentDoesNotBusyLoopWhileControlPlaneIsDown(t *testing.T) {
	var attempts int32
	client := &fakeControlPlaneClient{
		registerFunc: func(context.Context, clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error) {
			atomic.AddInt32(&attempts, 1)
			return clusterapi.RegisterResponse{}, errors.New("connection refused")
		},
	}
	agent := newFastTestAgent(t, client)
	agent.registrationBackoffBase = 20 * time.Millisecond
	agent.registrationBackoffMax = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	_ = agent.Run(ctx)

	// With a fixed 20ms backoff over a 90ms window, a well-behaved agent
	// retries roughly 4-5 times; a busy loop would retry thousands of times.
	got := atomic.LoadInt32(&attempts)
	if got == 0 {
		t.Fatal("no registration attempts were made")
	}
	if got > 20 {
		t.Errorf("registration attempts = %d in 90ms with a 20ms backoff, want a small number (not a busy loop)", got)
	}
}

func TestAgentReRegistersAfterUnknownNode404(t *testing.T) {
	var heartbeatCalls int32
	client := &fakeControlPlaneClient{
		heartbeatFunc: func(context.Context, uuid.UUID, clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error) {
			atomic.AddInt32(&heartbeatCalls, 1)
			return clusterapi.HeartbeatResponse{}, ErrNodeNotFound
		},
	}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_ = agent.Run(ctx)

	// Every heartbeat 404s, so every tick should trigger a fresh
	// registration attempt: registerCalls should track at least in step
	// with heartbeatCalls (one initial registration, plus one more per
	// 404'd heartbeat).
	registers := atomic.LoadInt32(&client.registerCalls)
	heartbeats := atomic.LoadInt32(&heartbeatCalls)
	if heartbeats == 0 {
		t.Fatal("no heartbeats were attempted")
	}
	if registers < 2 {
		t.Errorf("registerCalls = %d, want at least 2 (initial + at least one re-registration after a 404)", registers)
	}
}

func TestAgentOrdinaryHeartbeatFailureDoesNotTriggerReRegistration(t *testing.T) {
	client := &fakeControlPlaneClient{
		heartbeatFunc: func(context.Context, uuid.UUID, clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error) {
			return clusterapi.HeartbeatResponse{}, errors.New("connection refused")
		},
	}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = agent.Run(ctx)

	if got := atomic.LoadInt32(&client.registerCalls); got != 1 {
		t.Errorf("registerCalls = %d, want exactly 1 — an ordinary heartbeat failure must not trigger re-registration", got)
	}
}

func TestAgentRunReturnsPromptlyOnShutdown(t *testing.T) {
	client := &fakeControlPlaneClient{}
	agent := newFastTestAgent(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	time.Sleep(15 * time.Millisecond) // let it register and heartbeat at least once
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v on graceful shutdown, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of context cancellation — goroutine leak")
	}
}

func TestAgentRunFailsCleanlyWhenMachineInfoUnavailable(t *testing.T) {
	client := &fakeControlPlaneClient{}
	agent := New(testAgentConfig(t), testLogger(), client)
	agent.machineInfo = func(string) (MachineInfo, error) {
		return MachineInfo{}, errors.New("cannot determine memory capacity")
	}

	err := agent.Run(context.Background())
	if err == nil {
		t.Fatal("Run succeeded despite undiscoverable machine info, want an error")
	}
	if atomic.LoadInt32(&client.registerCalls) != 0 {
		t.Error("Register was called despite machine info discovery failing")
	}
}
