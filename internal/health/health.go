// Package health defines the small vocabulary the control plane uses to report
// whether it is alive and whether its dependencies are usable.
//
// The HTTP layer depends on the Checker interface rather than on a concrete
// database handle. That keeps readiness testable without a live PostgreSQL
// server, and lets later phases add further dependencies to the readiness probe
// without changing the handlers.
package health

import "context"

// Status values reported by the control plane health endpoints.
const (
	// StatusOK is reported by the liveness probe once the HTTP server is serving.
	StatusOK = "ok"
	// StatusReady is reported when every dependency answered successfully.
	StatusReady = "ready"
	// StatusNotReady is reported when at least one dependency is unavailable.
	StatusNotReady = "not ready"
)

// Checker reports whether a dependency is currently usable.
//
// Implementations must honour ctx and must not block past its deadline: a
// readiness probe that hangs is indistinguishable from an outage.
type Checker interface {
	// Check returns nil when the dependency is usable, or an error describing
	// why it is not. The error is for operators (logs), never for API clients.
	Check(ctx context.Context) error
}

// CheckerFunc adapts an ordinary function to the Checker interface.
type CheckerFunc func(ctx context.Context) error

// Check implements Checker.
func (f CheckerFunc) Check(ctx context.Context) error { return f(ctx) }
