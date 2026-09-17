package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/health"
)

// newTestRouter builds the real routing table with a stubbed dependency check,
// so the endpoints are exercised end to end without a PostgreSQL server.
func newTestRouter(checker health.Checker) http.Handler {
	return newRouter(&api{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		readiness:        checker,
		readinessTimeout: time.Second,
	})
}

func healthyChecker() health.Checker {
	return health.CheckerFunc(func(context.Context) error { return nil })
}

func failingChecker(err error) health.Checker {
	return health.CheckerFunc(func(context.Context) error { return err })
}

func do(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) statusResponse {
	t.Helper()
	var body statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	return body
}

func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeJSON)
	}
}

func TestHealthEndpointReturnsOK(t *testing.T) {
	rec := do(t, newTestRouter(healthyChecker()), http.MethodGet, "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertJSONContentType(t, rec)

	if got := decodeStatus(t, rec); got.Status != health.StatusOK {
		t.Errorf("status field = %q, want %q", got.Status, health.StatusOK)
	}
}

// The liveness probe must stay green while a dependency is down, so that a
// database outage never causes the control plane itself to be restarted.
func TestHealthEndpointIgnoresDatabaseFailure(t *testing.T) {
	router := newTestRouter(failingChecker(errors.New("connection refused")))

	rec := do(t, router, http.MethodGet, "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyEndpointWhenDatabaseAvailable(t *testing.T) {
	rec := do(t, newTestRouter(healthyChecker()), http.MethodGet, "/ready")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertJSONContentType(t, rec)

	body := decodeStatus(t, rec)
	if body.Status != health.StatusReady {
		t.Errorf("status field = %q, want %q", body.Status, health.StatusReady)
	}
	if body.Error != "" {
		t.Errorf("error field = %q, want it omitted", body.Error)
	}
}

func TestReadyEndpointWhenDatabaseUnavailable(t *testing.T) {
	router := newTestRouter(failingChecker(errors.New("dial tcp 127.0.0.1:5432: connection refused")))

	rec := do(t, router, http.MethodGet, "/ready")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	assertJSONContentType(t, rec)

	body := decodeStatus(t, rec)
	if body.Status != health.StatusNotReady {
		t.Errorf("status field = %q, want %q", body.Status, health.StatusNotReady)
	}
	if body.Error == "" {
		t.Error("error field is empty, want a client-safe reason")
	}
}

// The readiness body must never relay driver or credential detail to clients.
func TestReadyEndpointDoesNotLeakInternalDetail(t *testing.T) {
	secrets := []string{"super-secret-password", "postgres://nimbus", "5432"}
	underlying := errors.New("database: ping failed: postgres://nimbus:super-secret-password@localhost:5432/nimbus")

	rec := do(t, newTestRouter(failingChecker(underlying)), http.MethodGet, "/ready")

	body := rec.Body.String()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("response body leaked %q: %s", secret, body)
		}
	}
}

// A stalled dependency must not hold the probe open: the handler is responsible
// for giving the checker a deadline.
func TestReadyEndpointBoundsTheCheckWithADeadline(t *testing.T) {
	var hadDeadline bool
	checker := health.CheckerFunc(func(ctx context.Context) error {
		_, hadDeadline = ctx.Deadline()
		return nil
	})

	do(t, newTestRouter(checker), http.MethodGet, "/ready")

	if !hadDeadline {
		t.Error("readiness checker received a context without a deadline")
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	rec := do(t, newTestRouter(healthyChecker()), http.MethodGet, "/does-not-exist")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	assertJSONContentType(t, rec)

	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	if body.Error == "" {
		t.Error("error field is empty, want a message")
	}
}

func TestWrongMethodReturnsJSONMethodNotAllowed(t *testing.T) {
	for _, path := range []string{"/health", "/ready"} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, newTestRouter(healthyChecker()), http.MethodPost, path)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
			assertJSONContentType(t, rec)
			if got := rec.Header().Get("Allow"); got != http.MethodGet {
				t.Errorf("Allow = %q, want %q", got, http.MethodGet)
			}
		})
	}
}
