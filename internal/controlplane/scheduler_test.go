package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/scheduler"
	"github.com/dhananjaiyadav1234/Nimbus/internal/schedulerapi"
)

// stubSchedulerService is a SchedulerService test double, mirroring
// stubNodeService/stubDeploymentService.
type stubSchedulerService struct {
	scheduleFunc   func(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error)
	placementsFunc func(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error)
}

func (s *stubSchedulerService) Schedule(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error) {
	if s.scheduleFunc != nil {
		return s.scheduleFunc(ctx, deploymentID)
	}
	return []scheduler.Placement{{DeploymentID: deploymentID, ReplicaIndex: 0, NodeID: uuid.New(), CPU: 1, MemoryBytes: 1024}}, nil
}

func (s *stubSchedulerService) Placements(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error) {
	if s.placementsFunc != nil {
		return s.placementsFunc(ctx, deploymentID)
	}
	return nil, nil
}

func newSchedulerTestRouter(schedulerSvc SchedulerService) http.Handler {
	return newRouter(&api{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		readinessTimeout: time.Second,
		nodes:            &stubNodeService{},
		deployments:      &stubDeploymentService{},
		scheduler:        schedulerSvc,
	})
}

// --- POST /deployments/{id}/schedule ----------------------------------------

func TestHandleScheduleDeploymentSuccess(t *testing.T) {
	deploymentID := uuid.New()
	nodeID := uuid.New()
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, id uuid.UUID) ([]scheduler.Placement, error) {
			if id != deploymentID {
				t.Errorf("Schedule called with %s, want %s", id, deploymentID)
			}
			return []scheduler.Placement{
				{DeploymentID: id, ReplicaIndex: 0, NodeID: nodeID, CPU: 1, MemoryBytes: 512},
				{DeploymentID: id, ReplicaIndex: 1, NodeID: nodeID, CPU: 1, MemoryBytes: 512},
			}, nil
		},
	}

	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+deploymentID.String()+"/schedule", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	var resp schedulerapi.ScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.DeploymentID != deploymentID.String() {
		t.Errorf("DeploymentID = %q, want %q", resp.DeploymentID, deploymentID.String())
	}
	if len(resp.Placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(resp.Placements))
	}
	if resp.Placements[0].NodeID != nodeID.String() || resp.Placements[0].CPU != 1 || resp.Placements[0].MemoryBytes != 512 {
		t.Errorf("placement[0] = %+v, unexpected values", resp.Placements[0])
	}
}

func TestHandleScheduleDeploymentMalformedUUID(t *testing.T) {
	rec := doDeploymentReq(t, newSchedulerTestRouter(&stubSchedulerService{}), http.MethodPost, "/deployments/not-a-uuid/schedule", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScheduleDeploymentNotFound(t *testing.T) {
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			return nil, scheduler.ErrDeploymentNotFound
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+uuid.New().String()+"/schedule", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleScheduleDeploymentInsufficientCapacity(t *testing.T) {
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			return nil, scheduler.ErrInsufficientCapacity
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+uuid.New().String()+"/schedule", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if body.Error == "" {
		t.Error("error response has an empty message")
	}
}

func TestHandleScheduleDeploymentAlreadyScheduledSucceeds(t *testing.T) {
	// Re-scheduling an already-fully-scheduled deployment is a successful
	// no-op (idempotency), not an error — the handler has no special case
	// for this because Service.Schedule itself returns success with the
	// unchanged placement set.
	deploymentID := uuid.New()
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, id uuid.UUID) ([]scheduler.Placement, error) {
			return []scheduler.Placement{{DeploymentID: id, ReplicaIndex: 0, NodeID: uuid.New(), CPU: 1, MemoryBytes: 1024}}, nil
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+deploymentID.String()+"/schedule", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleScheduleDeploymentPartiallyScheduled(t *testing.T) {
	deploymentID := uuid.New()
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, id uuid.UUID) ([]scheduler.Placement, error) {
			return []scheduler.Placement{
				{DeploymentID: id, ReplicaIndex: 0, NodeID: uuid.New(), CPU: 1, MemoryBytes: 1024},
				{DeploymentID: id, ReplicaIndex: 1, NodeID: uuid.New(), CPU: 1, MemoryBytes: 1024},
			}, nil
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+deploymentID.String()+"/schedule", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp schedulerapi.ScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Placements) != 2 {
		t.Errorf("got %d placements, want 2", len(resp.Placements))
	}
}

func TestHandleScheduleDeploymentInternalErrorDoesNotLeakDetail(t *testing.T) {
	stub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			return nil, errPgConnRefusedForTest
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodPost, "/deployments/"+uuid.New().String()+"/schedule", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if body.Error != "unable to schedule deployment" {
		t.Errorf("error message = %q, leaks internal detail (want the fixed safe message)", body.Error)
	}
}

func TestHandleScheduleDeploymentWrongMethod(t *testing.T) {
	rec := doDeploymentReq(t, newSchedulerTestRouter(&stubSchedulerService{}), http.MethodGet, "/deployments/"+uuid.New().String()+"/schedule", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// --- GET /deployments/{id}/placements ---------------------------------------

func TestHandleGetPlacementsSuccess(t *testing.T) {
	deploymentID := uuid.New()
	nodeID := uuid.New()
	stub := &stubSchedulerService{
		placementsFunc: func(_ context.Context, id uuid.UUID) ([]scheduler.Placement, error) {
			if id != deploymentID {
				t.Errorf("Placements called with %s, want %s", id, deploymentID)
			}
			return []scheduler.Placement{{DeploymentID: id, ReplicaIndex: 0, NodeID: nodeID, CPU: 2, MemoryBytes: 2048}}, nil
		},
	}

	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodGet, "/deployments/"+deploymentID.String()+"/placements", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp schedulerapi.PlacementsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Placements) != 1 || resp.Placements[0].NodeID != nodeID.String() {
		t.Errorf("placements = %+v, unexpected", resp.Placements)
	}
}

func TestHandleGetPlacementsNoPlacementsYetReturnsEmptyArray(t *testing.T) {
	stub := &stubSchedulerService{
		placementsFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) { return nil, nil },
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodGet, "/deployments/"+uuid.New().String()+"/placements", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !jsonContains(t, rec.Body.Bytes(), `"placements":[]`) {
		t.Errorf("body = %s, want an empty array (not null) for placements", rec.Body.String())
	}
}

func TestHandleGetPlacementsDeploymentNotFound(t *testing.T) {
	stub := &stubSchedulerService{
		placementsFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			return nil, scheduler.ErrDeploymentNotFound
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodGet, "/deployments/"+uuid.New().String()+"/placements", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleGetPlacementsMalformedUUID(t *testing.T) {
	rec := doDeploymentReq(t, newSchedulerTestRouter(&stubSchedulerService{}), http.MethodGet, "/deployments/not-a-uuid/placements", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleGetPlacementsRepositoryFailure(t *testing.T) {
	stub := &stubSchedulerService{
		placementsFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			return nil, errPgConnRefusedForTest
		},
	}
	rec := doDeploymentReq(t, newSchedulerTestRouter(stub), http.MethodGet, "/deployments/"+uuid.New().String()+"/placements", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if body.Error != "unable to get placements" {
		t.Errorf("error message = %q, leaks internal detail", body.Error)
	}
}

// --- Deployment creation must remain persistence-only -----------------------

func TestCreateDeploymentDoesNotInvokeScheduler(t *testing.T) {
	schedulerCalled := false
	schedulerStub := &stubSchedulerService{
		scheduleFunc: func(_ context.Context, _ uuid.UUID) ([]scheduler.Placement, error) {
			schedulerCalled = true
			return nil, nil
		},
	}
	router := newRouter(&api{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		readinessTimeout: time.Second,
		nodes:            &stubNodeService{},
		deployments:      &stubDeploymentService{},
		scheduler:        schedulerStub,
	})

	rec := doDeploymentReq(t, router, http.MethodPost, "/deployments", validManifest())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if schedulerCalled {
		t.Error("POST /deployments invoked the scheduler — deployment creation must remain persistence-only")
	}
}

// errPgConnRefusedForTest stands in for an opaque internal failure (e.g. a
// database error) a handler must never expose verbatim to a client.
var errPgConnRefusedForTest = &opaqueTestError{"dial tcp 10.0.0.5:5432: connect: connection refused"}

type opaqueTestError struct{ msg string }

func (e *opaqueTestError) Error() string { return e.msg }

func jsonContains(t *testing.T, body []byte, substr string) bool {
	t.Helper()
	return strings.Contains(string(body), substr)
}
