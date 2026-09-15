package cluster

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Monitor periodically sweeps the cluster for nodes that have stopped
// heartbeating and moves them to StatusNotReady. It is Nimbus's only
// background goroutine in Phase 1.2, and it owns no membership state of its
// own — every sweep re-reads and re-writes PostgreSQL through Service, so a
// Control Plane restart never loses track of which nodes were failing.
type Monitor struct {
	service  *Service
	logger   *slog.Logger
	interval time.Duration
}

// NewMonitor builds a Monitor that sweeps at the Service's configured
// heartbeat interval — the same cadence agents are told to heartbeat at, so
// a missed heartbeat is detected within one extra tick of its deadline.
func NewMonitor(service *Service, logger *slog.Logger) *Monitor {
	return &Monitor{service: service, logger: logger, interval: service.HeartbeatInterval()}
}

// Run sweeps on every tick until ctx is cancelled, then returns. Callers
// should run it in its own goroutine and wait for that goroutine to exit
// (for example with a sync.WaitGroup) before considering shutdown complete,
// so it never leaks past the Control Plane's own lifetime.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep(ctx, time.Now())
		}
	}
}

// sweep runs one failure-detection pass as of now. now is a parameter,
// rather than read from the system clock internally, so tests can drive it
// with fixed timestamps instead of sleeping through real heartbeat timeouts.
// It only logs the nodes that actually transitioned — an unchanged cluster
// produces no log output at all, so the monitor cannot spam logs merely by
// ticking.
func (m *Monitor) sweep(ctx context.Context, now time.Time) {
	stale, err := m.service.DetectStaleNodes(ctx, now)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Shutdown raced a sweep in flight: ctx was cancelled while the
			// database call was outstanding. That is normal, expected
			// behaviour during graceful shutdown, not a real failure, so it
			// is not worth an ERROR-level log — Run's own context.Done()
			// case logs nothing further and simply returns on the next loop
			// iteration.
			return
		}
		m.logger.ErrorContext(ctx, "membership sweep failed", slog.String("error", err.Error()))
		return
	}

	for _, node := range stale {
		fields := []any{
			slog.String("node_id", node.ID.String()),
			slog.String("hostname", node.Hostname),
			slog.Duration("timeout", m.service.HeartbeatTimeout()),
		}
		if node.LastHeartbeatAt != nil {
			fields = append(fields, slog.Time("last_heartbeat_at", *node.LastHeartbeatAt))
		}
		m.logger.InfoContext(ctx, "node marked NotReady", fields...)
	}
}
