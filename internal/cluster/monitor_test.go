package cluster

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestMonitorRunStopsOnContextCancellation(t *testing.T) {
	repo := newFakeRepository()
	svc, err := NewService(repo, 5*time.Millisecond, 3)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	monitor := NewMonitor(svc, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	// Let it tick a few times, then cancel and require it to exit promptly —
	// this is what proves the goroutine does not leak past shutdown.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of context cancellation")
	}
}

func TestMonitorSweepLogsOnlyTransitions(t *testing.T) {
	repo := newFakeRepository()
	svc, err := NewService(repo, time.Second, 3) // timeout = 3s
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	fresh := validInput()
	fresh.Hostname = "fresh-node"
	if _, err := svc.Register(context.Background(), fresh); err != nil {
		t.Fatalf("Register(fresh): %v", err)
	}

	stale := validInput()
	stale.Hostname = "stale-node"
	staleResult, err := svc.Register(context.Background(), stale)
	if err != nil {
		t.Fatalf("Register(stale): %v", err)
	}

	var logs bytes.Buffer
	monitor := NewMonitor(svc, slog.New(slog.NewTextHandler(&logs, nil)))

	// Sweep far enough past the timeout that "stale-node" (registered at
	// `start`, never heartbeated again) is overdue, while "fresh-node" would
	// be too if it hadn't just been registered — but both were registered at
	// the same instant, so to isolate the transition we heartbeat fresh-node
	// forward first.
	if _, _, err := svc.repo.RecordHeartbeat(context.Background(), mustGet(t, svc, "fresh-node").ID, start.Add(2*time.Second)); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	monitor.sweep(context.Background(), start.Add(4*time.Second))

	out := logs.String()
	if !strings.Contains(out, "node marked NotReady") {
		t.Errorf("log output missing the NotReady transition: %s", out)
	}
	if !strings.Contains(out, staleResult.Node.ID.String()) {
		t.Errorf("log output does not name the stale node: %s", out)
	}
	if strings.Contains(out, "fresh-node") {
		t.Errorf("log output mentions the fresh node, want only the transitioned one: %s", out)
	}
}

// ctxCancelledRepository wraps a fakeRepository but makes MarkStale behave
// the way a real database call does when its context is already cancelled —
// returning that error — so sweep's handling of a shutdown race (context
// cancelled while a sweep's DB call is outstanding) can be tested without a
// real PostgreSQL server.
type ctxCancelledRepository struct {
	*fakeRepository
}

func (r ctxCancelledRepository) MarkStale(ctx context.Context, cutoff, now time.Time) ([]Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.fakeRepository.MarkStale(ctx, cutoff, now)
}

func TestMonitorSweepDoesNotLogErrorForContextCancellation(t *testing.T) {
	repo := ctxCancelledRepository{newFakeRepository()}
	svc, err := NewService(repo, time.Second, 3)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	var logs bytes.Buffer
	monitor := NewMonitor(svc, slog.New(slog.NewTextHandler(&logs, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled, simulating a shutdown race

	monitor.sweep(ctx, time.Now())

	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("sweep logged an ERROR for a context-cancellation during shutdown, want silence: %s", logs.String())
	}
}

func mustGet(t *testing.T, svc *Service, hostname string) Node {
	t.Helper()
	nodes, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, n := range nodes {
		if n.Hostname == hostname {
			return n
		}
	}
	t.Fatalf("no node with hostname %q", hostname)
	return Node{}
}
