package controlplane

import (
	"bytes"
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

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/cluster"
	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

// stubNodeService is a NodeService test double. Each method has an
// overridable function field defaulting to a reasonable success response, so
// individual tests only need to set the one behaviour they're exercising —
// mirroring how health.CheckerFunc is used for the readiness tests.
type stubNodeService struct {
	registerFunc  func(ctx context.Context, in cluster.RegisterInput) (cluster.RegisterResult, error)
	heartbeatFunc func(ctx context.Context, id uuid.UUID) (cluster.HeartbeatResult, error)
	getFunc       func(ctx context.Context, id uuid.UUID) (cluster.Node, error)
	listFunc      func(ctx context.Context) ([]cluster.Node, error)
}

func (s *stubNodeService) Register(ctx context.Context, in cluster.RegisterInput) (cluster.RegisterResult, error) {
	if s.registerFunc != nil {
		return s.registerFunc(ctx, in)
	}
	return cluster.RegisterResult{
		Node:              cluster.Node{ID: uuid.New(), Hostname: in.Hostname, Status: cluster.StatusReady},
		HeartbeatInterval: 10 * time.Second,
	}, nil
}

func (s *stubNodeService) Heartbeat(ctx context.Context, id uuid.UUID) (cluster.HeartbeatResult, error) {
	if s.heartbeatFunc != nil {
		return s.heartbeatFunc(ctx, id)
	}
	return cluster.HeartbeatResult{Node: cluster.Node{ID: id, Status: cluster.StatusReady}}, nil
}

func (s *stubNodeService) Get(ctx context.Context, id uuid.UUID) (cluster.Node, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, id)
	}
	return cluster.Node{ID: id, Status: cluster.StatusReady}, nil
}

func (s *stubNodeService) List(ctx context.Context) ([]cluster.Node, error) {
	if s.listFunc != nil {
		return s.listFunc(ctx)
	}
	return nil, nil
}

func newNodesTestRouter(nodes NodeService) http.Handler {
	return newRouter(&api{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		readiness:        nil, // not exercised by these tests
		readinessTimeout: time.Second,
		nodes:            nodes,
	})
}

func doJSON(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, reader))
	return rec
}

func validRegisterRequest() clusterapi.RegisterRequest {
	return clusterapi.RegisterRequest{
		Hostname:            "worker-01",
		OS:                  "linux",
		Architecture:        "amd64",
		CPUCapacity:         8,
		MemoryCapacityBytes: 17179869184,
		AgentVersion:        "0.1.0",
	}
}

// --- Registration ----------------------------------------------------------

func TestHandleRegisterNodeSuccess(t *testing.T) {
	stub := &stubNodeService{}
	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/register", validRegisterRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	var resp clusterapi.RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.NodeID == "" {
		t.Error("NodeID is empty")
	}
	if resp.Status != string(cluster.StatusReady) {
		t.Errorf("Status = %q, want %q", resp.Status, cluster.StatusReady)
	}
	if resp.HeartbeatIntervalSeconds != 10 {
		t.Errorf("HeartbeatIntervalSeconds = %d, want 10", resp.HeartbeatIntervalSeconds)
	}
}

func TestHandleRegisterNodeMalformedJSON(t *testing.T) {
	stub := &stubNodeService{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/nodes/register", strings.NewReader("{not json"))
	newNodesTestRouter(stub).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertJSONContentType(t, rec)
}

func TestHandleRegisterNodeMissingFields(t *testing.T) {
	stub := &stubNodeService{
		registerFunc: func(_ context.Context, in cluster.RegisterInput) (cluster.RegisterResult, error) {
			return cluster.RegisterResult{}, in.Validate()
		},
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/register", clusterapi.RegisterRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Error == "" {
		t.Error("error message is empty")
	}
}

func TestHandleRegisterNodeInvalidNodeID(t *testing.T) {
	stub := &stubNodeService{}
	req := validRegisterRequest()
	req.NodeID = "not-a-uuid"

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/register", req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleRegisterNodeWithExistingIDIsPassedThrough(t *testing.T) {
	existingID := uuid.New()
	var gotID uuid.UUID
	stub := &stubNodeService{
		registerFunc: func(_ context.Context, in cluster.RegisterInput) (cluster.RegisterResult, error) {
			gotID = in.NodeID
			return cluster.RegisterResult{Node: cluster.Node{ID: in.NodeID, Status: cluster.StatusReady}}, nil
		},
	}

	req := validRegisterRequest()
	req.NodeID = existingID.String()
	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/register", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotID != existingID {
		t.Errorf("service received node_id %s, want %s", gotID, existingID)
	}
}

func TestHandleRegisterNodeInternalErrorDoesNotLeakDetail(t *testing.T) {
	stub := &stubNodeService{
		registerFunc: func(context.Context, cluster.RegisterInput) (cluster.RegisterResult, error) {
			return cluster.RegisterResult{}, errors.New("pq: connection to server at \"10.0.0.5\" failed: password authentication failed for user \"nimbus\"")
		},
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/register", validRegisterRequest())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("response body leaked internal detail: %s", rec.Body.String())
	}
}

// --- Heartbeat ---------------------------------------------------------------

func TestHandleHeartbeatSuccess(t *testing.T) {
	id := uuid.New()
	stub := &stubNodeService{
		heartbeatFunc: func(_ context.Context, gotID uuid.UUID) (cluster.HeartbeatResult, error) {
			if gotID != id {
				t.Errorf("service received id %s, want %s", gotID, id)
			}
			return cluster.HeartbeatResult{Node: cluster.Node{ID: id, Status: cluster.StatusReady}}, nil
		},
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/"+id.String()+"/heartbeat", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	var resp clusterapi.HeartbeatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Status != string(cluster.StatusReady) {
		t.Errorf("Status = %q, want %q", resp.Status, cluster.StatusReady)
	}
}

func TestHandleHeartbeatWithOptionalTimestampBody(t *testing.T) {
	stub := &stubNodeService{}
	ts := time.Now()
	body := clusterapi.HeartbeatRequest{Timestamp: &ts}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/"+uuid.New().String()+"/heartbeat", body)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleHeartbeatUnknownNodeReturns404(t *testing.T) {
	stub := &stubNodeService{
		heartbeatFunc: func(context.Context, uuid.UUID) (cluster.HeartbeatResult, error) {
			return cluster.HeartbeatResult{}, cluster.ErrNotFound
		},
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/"+uuid.New().String()+"/heartbeat", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	assertJSONContentType(t, rec)
}

func TestHandleHeartbeatInvalidNodeID(t *testing.T) {
	stub := &stubNodeService{}
	rec := doJSON(t, newNodesTestRouter(stub), http.MethodPost, "/nodes/not-a-uuid/heartbeat", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- GET /nodes/{id} ---------------------------------------------------------

func TestHandleGetNodeSuccess(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	stub := &stubNodeService{
		getFunc: func(_ context.Context, gotID uuid.UUID) (cluster.Node, error) {
			return cluster.Node{
				ID: gotID, Hostname: "worker-01", Status: cluster.StatusReady,
				OS: "linux", Architecture: "amd64", CPUCapacity: 8,
				MemoryCapacityBytes: 17179869184, AgentVersion: "0.1.0",
				LastHeartbeatAt: &now, RegisteredAt: now, UpdatedAt: now,
			}, nil
		},
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodGet, "/nodes/"+id.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var dto clusterapi.NodeDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if dto.ID != id.String() {
		t.Errorf("ID = %q, want %q", dto.ID, id.String())
	}
	if dto.Hostname != "worker-01" {
		t.Errorf("Hostname = %q, want %q", dto.Hostname, "worker-01")
	}
}

func TestHandleGetNodeUnknownReturns404(t *testing.T) {
	stub := &stubNodeService{
		getFunc: func(context.Context, uuid.UUID) (cluster.Node, error) { return cluster.Node{}, cluster.ErrNotFound },
	}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodGet, "/nodes/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleGetNodeInvalidID(t *testing.T) {
	stub := &stubNodeService{}
	rec := doJSON(t, newNodesTestRouter(stub), http.MethodGet, "/nodes/not-a-uuid", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- GET /nodes ----------------------------------------------------------------

func TestHandleListNodesSuccess(t *testing.T) {
	now := time.Now()
	nodes := []cluster.Node{
		{ID: uuid.New(), Hostname: "alice", Status: cluster.StatusReady, RegisteredAt: now, UpdatedAt: now},
		{ID: uuid.New(), Hostname: "bob", Status: cluster.StatusNotReady, RegisteredAt: now, UpdatedAt: now},
	}
	stub := &stubNodeService{listFunc: func(context.Context) ([]cluster.Node, error) { return nodes, nil }}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodGet, "/nodes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	var resp clusterapi.ListNodesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("Nodes has %d entries, want 2", len(resp.Nodes))
	}
	if resp.Nodes[0].Hostname != "alice" || resp.Nodes[1].Hostname != "bob" {
		t.Errorf("unexpected node order/content: %+v", resp.Nodes)
	}
}

func TestHandleListNodesEmptyClusterReturnsEmptyArrayNotNull(t *testing.T) {
	stub := &stubNodeService{listFunc: func(context.Context) ([]cluster.Node, error) { return nil, nil }}

	rec := doJSON(t, newNodesTestRouter(stub), http.MethodGet, "/nodes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), `"nodes":null`) {
		t.Errorf("response encodes nodes as null, want an empty array: %s", rec.Body.String())
	}
}

// --- Wrong method on node routes -----------------------------------------------

func TestNodeRoutesRejectWrongMethod(t *testing.T) {
	stub := &stubNodeService{}
	router := newNodesTestRouter(stub)

	tests := []struct{ method, path string }{
		{http.MethodGet, "/nodes/register"},
		{http.MethodGet, "/nodes/" + uuid.New().String() + "/heartbeat"},
		{http.MethodPost, "/nodes"},
	}
	for _, tc := range tests {
		rec := doJSON(t, router, tc.method, tc.path, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}
