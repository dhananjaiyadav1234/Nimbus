package controlplane

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/health"
)

// contentTypeJSON is the media type every control plane response carries.
// JSON is always UTF-8, so no charset parameter is needed.
const contentTypeJSON = "application/json"

// statusResponse is the body returned by the health and readiness probes.
type statusResponse struct {
	Status string `json:"status"`
	// Error carries a short, client-safe reason when Status is not healthy.
	Error string `json:"error,omitempty"`
}

// errorResponse is the shape of every error body Nimbus returns.
type errorResponse struct {
	Error string `json:"error"`
}

// api holds the dependencies the HTTP handlers need. It depends on
// health.Checker rather than on a database handle, so the readiness path can
// be exercised without a live PostgreSQL server, and on the narrow
// NodeService interface (see nodes.go) rather than *cluster.Service
// directly, for the same reason.
type api struct {
	logger *slog.Logger
	// readiness is the dependency check behind GET /ready.
	readiness health.Checker
	// readinessTimeout bounds a single readiness check so a stalled database
	// cannot hold the probe open indefinitely.
	readinessTimeout time.Duration
	// nodes is the cluster-membership service behind every /nodes* route.
	nodes NodeService
}

// newRouter wires every Phase 1.1 and Phase 1.2 endpoint.
//
// Routes are registered twice: once scoped to a method, and once unscoped so
// that a wrong method produces a JSON 405 instead of the standard library's
// plain-text response. The method-scoped pattern is the more specific of the
// two and wins whenever its method matches.
func newRouter(a *api) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", a.handleHealth)
	mux.Handle("/health", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /ready", a.handleReady)
	mux.Handle("/ready", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("POST /nodes/register", a.handleRegisterNode)
	// GET /nodes/register is registered explicitly, rather than as the
	// unscoped "/nodes/register" catch-all used elsewhere in this file,
	// because ServeMux cannot statically order an unscoped (all-methods)
	// literal pattern against the wildcard sibling "GET /nodes/{id}" — it
	// panics at startup with "conflicts with" if asked to. Scoping this
	// fallback to GET keeps it exactly as specific as that wildcard route,
	// which resolves the ambiguity in the literal path's favour.
	mux.Handle("GET /nodes/register", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /nodes/{id}/heartbeat", a.handleHeartbeat)
	mux.Handle("/nodes/{id}/heartbeat", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("GET /nodes/{id}", a.handleGetNode)
	mux.Handle("/nodes/{id}", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /nodes", a.handleListNodes)
	mux.Handle("/nodes", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("/", handleNotFound)

	return a.logRequests(mux)
}

// handleHealth is the liveness probe. Answering at all is the signal: it
// deliberately does not touch the database, so a database outage never causes
// an orchestrator to restart an otherwise healthy process.
func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(r.Context(), w, http.StatusOK, statusResponse{Status: health.StatusOK})
}

// handleReady is the readiness probe. It reports ready only once every
// dependency — currently just PostgreSQL — answers successfully.
func (a *api) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), a.readinessTimeout)
	defer cancel()

	if err := a.readiness.Check(ctx); err != nil {
		// The underlying error can name hosts and driver internals, so it goes
		// to the log for operators and never into the response body.
		a.logger.WarnContext(ctx, "readiness check failed", slog.String("error", err.Error()))
		a.writeJSON(ctx, w, http.StatusServiceUnavailable, statusResponse{
			Status: health.StatusNotReady,
			Error:  "database is unavailable",
		})
		return
	}

	a.writeJSON(ctx, w, http.StatusOK, statusResponse{Status: health.StatusReady})
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSONBody(w, http.StatusNotFound, errorResponse{Error: "resource not found"})
}

// methodNotAllowed returns a handler that rejects every request with a JSON 405
// advertising the methods the path does support.
func methodNotAllowed(allowed ...string) http.Handler {
	allow := strings.Join(allowed, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeJSONBody(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
	})
}

// writeJSON serialises payload, logging any encoding failure. The response
// headers are already committed by then, so there is nothing to tell the client.
func (a *api) writeJSON(ctx context.Context, w http.ResponseWriter, status int, payload any) {
	if err := writeJSONBody(w, status, payload); err != nil {
		a.logger.ErrorContext(ctx, "writing response body failed", slog.String("error", err.Error()))
	}
}

func writeJSONBody(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(payload)
}

// logRequests emits one structured record per request. Probes are logged at
// debug so that a one-second liveness poll does not drown out real events.
func (a *api) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		level := slog.LevelDebug
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelWarn
		}
		a.logger.Log(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(started)),
		)
	})
}

// statusRecorder captures the status code so it can be logged.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
