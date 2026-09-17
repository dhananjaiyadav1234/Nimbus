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

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
)

// stubDeploymentService is a DeploymentService test double, mirroring
// stubNodeService: each method has an overridable function field defaulting
// to a reasonable success response.
type stubDeploymentService struct {
	createFunc func(ctx context.Context, in deployment.CreateInput) (deployment.Deployment, error)
	getFunc    func(ctx context.Context, id uuid.UUID) (deployment.Deployment, error)
	listFunc   func(ctx context.Context) ([]deployment.Deployment, error)
	deleteFunc func(ctx context.Context, id uuid.UUID) error
}

func (s *stubDeploymentService) Create(ctx context.Context, in deployment.CreateInput) (deployment.Deployment, error) {
	if s.createFunc != nil {
		return s.createFunc(ctx, in)
	}
	now := time.Now()
	return deployment.Deployment{
		ID: uuid.New(), Name: in.Name, Image: in.Image, Replicas: in.Replicas,
		CPU: in.CPU, MemoryBytes: in.MemoryBytes, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *stubDeploymentService) Get(ctx context.Context, id uuid.UUID) (deployment.Deployment, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, id)
	}
	now := time.Now()
	return deployment.Deployment{ID: id, Name: "web", Image: "nginx:latest", Replicas: 1, CPU: 1, MemoryBytes: 1 << 20, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *stubDeploymentService) List(ctx context.Context) ([]deployment.Deployment, error) {
	if s.listFunc != nil {
		return s.listFunc(ctx)
	}
	return nil, nil
}

func (s *stubDeploymentService) Delete(ctx context.Context, id uuid.UUID) error {
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, id)
	}
	return nil
}

func newDeploymentsTestRouter(deployments DeploymentService) http.Handler {
	return newRouter(&api{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		readinessTimeout: time.Second,
		nodes:            &stubNodeService{},
		deployments:      deployments,
	})
}

func validManifest() deploymentapi.Manifest {
	return deploymentapi.Manifest{
		APIVersion: deploymentapi.SupportedAPIVersion,
		Kind:       deploymentapi.DeploymentKind,
		Metadata:   deploymentapi.Metadata{Name: "web"},
		Spec: deploymentapi.Spec{
			Image:    "nginx:latest",
			Replicas: 2,
			Resources: &deploymentapi.Resources{
				CPU:    1,
				Memory: "512Mi",
			},
		},
	}
}

func doDeploymentReq(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
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

// --- Create ------------------------------------------------------------------

func TestHandleCreateDeploymentSuccess(t *testing.T) {
	var gotInput deployment.CreateInput
	stub := &stubDeploymentService{
		createFunc: func(_ context.Context, in deployment.CreateInput) (deployment.Deployment, error) {
			gotInput = in
			now := time.Now()
			return deployment.Deployment{ID: uuid.New(), Name: in.Name, Image: in.Image, Replicas: in.Replicas, CPU: in.CPU, MemoryBytes: in.MemoryBytes, CreatedAt: now, UpdatedAt: now}, nil
		},
	}

	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodPost, "/deployments", validManifest())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	if gotInput.Name != "web" || gotInput.Image != "nginx:latest" || gotInput.Replicas != 2 || gotInput.CPU != 1 {
		t.Errorf("unexpected CreateInput passed to service: %+v", gotInput)
	}
	if gotInput.MemoryBytes != 512*1024*1024 {
		t.Errorf("MemoryBytes = %d, want %d (512Mi normalized to bytes)", gotInput.MemoryBytes, 512*1024*1024)
	}

	var dto deploymentapi.DeploymentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if dto.Name != "web" {
		t.Errorf("response Name = %q, want %q", dto.Name, "web")
	}
}

func TestHandleCreateDeploymentMalformedJSON(t *testing.T) {
	stub := &stubDeploymentService{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/deployments", strings.NewReader("{not json"))
	newDeploymentsTestRouter(stub).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateDeploymentWrongAPIVersion(t *testing.T) {
	m := validManifest()
	m.APIVersion = "nimbus/v2"
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateDeploymentWrongKind(t *testing.T) {
	m := validManifest()
	m.Kind = "Pod"
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateDeploymentMissingName(t *testing.T) {
	m := validManifest()
	m.Metadata.Name = ""
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateDeploymentMissingResources(t *testing.T) {
	m := validManifest()
	m.Spec.Resources = nil
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleCreateDeploymentInvalidMemoryQuantity(t *testing.T) {
	m := validManifest()
	m.Spec.Resources.Memory = "not-a-quantity"
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateDeploymentValidationErrorFromService(t *testing.T) {
	stub := &stubDeploymentService{
		createFunc: func(_ context.Context, in deployment.CreateInput) (deployment.Deployment, error) {
			return deployment.Deployment{}, in.Validate()
		},
	}
	m := validManifest()
	m.Metadata.Name = "Invalid Name!"
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleCreateDeploymentDuplicateNameReturns409(t *testing.T) {
	stub := &stubDeploymentService{
		createFunc: func(context.Context, deployment.CreateInput) (deployment.Deployment, error) {
			return deployment.Deployment{}, deployment.ErrAlreadyExists
		},
	}
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodPost, "/deployments", validManifest())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	assertJSONContentType(t, rec)
}

func TestHandleCreateDeploymentInternalErrorDoesNotLeakDetail(t *testing.T) {
	stub := &stubDeploymentService{
		createFunc: func(context.Context, deployment.CreateInput) (deployment.Deployment, error) {
			return deployment.Deployment{}, errors.New("pq: connection to server at \"10.0.0.9\" failed: password authentication failed")
		},
	}
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodPost, "/deployments", validManifest())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.9") || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("response body leaked internal detail: %s", rec.Body.String())
	}
}

func TestHandleCreateDeploymentBodyTooLarge(t *testing.T) {
	m := validManifest()
	m.Metadata.Name = strings.Repeat("a", maxDeploymentBodyBytes+1)
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodPost, "/deployments", m)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (oversized body should be rejected, not read fully)", rec.Code, http.StatusBadRequest)
	}
}

// --- Get -----------------------------------------------------------------------

func TestHandleGetDeploymentSuccess(t *testing.T) {
	id := uuid.New()
	stub := &stubDeploymentService{
		getFunc: func(_ context.Context, gotID uuid.UUID) (deployment.Deployment, error) {
			now := time.Now()
			return deployment.Deployment{ID: gotID, Name: "web", Image: "nginx:latest", Replicas: 2, CPU: 1, MemoryBytes: 1 << 29, CreatedAt: now, UpdatedAt: now}, nil
		},
	}

	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodGet, "/deployments/"+id.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var dto deploymentapi.DeploymentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if dto.ID != id.String() {
		t.Errorf("ID = %q, want %q", dto.ID, id.String())
	}
}

func TestHandleGetDeploymentUnknownReturns404(t *testing.T) {
	stub := &stubDeploymentService{
		getFunc: func(context.Context, uuid.UUID) (deployment.Deployment, error) {
			return deployment.Deployment{}, deployment.ErrNotFound
		},
	}
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodGet, "/deployments/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleGetDeploymentInvalidID(t *testing.T) {
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodGet, "/deployments/not-a-uuid", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- List ----------------------------------------------------------------------

func TestHandleListDeploymentsSuccess(t *testing.T) {
	now := time.Now()
	deployments := []deployment.Deployment{
		{ID: uuid.New(), Name: "api", Image: "api:latest", Replicas: 1, CPU: 1, MemoryBytes: 1 << 20, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), Name: "web", Image: "nginx:latest", Replicas: 2, CPU: 1, MemoryBytes: 1 << 20, CreatedAt: now, UpdatedAt: now},
	}
	stub := &stubDeploymentService{listFunc: func(context.Context) ([]deployment.Deployment, error) { return deployments, nil }}

	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodGet, "/deployments", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp deploymentapi.ListDeploymentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Deployments) != 2 {
		t.Fatalf("Deployments has %d entries, want 2", len(resp.Deployments))
	}
}

func TestHandleListDeploymentsEmptyReturnsEmptyArrayNotNull(t *testing.T) {
	stub := &stubDeploymentService{listFunc: func(context.Context) ([]deployment.Deployment, error) { return nil, nil }}
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodGet, "/deployments", nil)
	if strings.Contains(rec.Body.String(), `"deployments":null`) {
		t.Errorf("response encodes deployments as null, want an empty array: %s", rec.Body.String())
	}
}

// --- Delete --------------------------------------------------------------------

func TestHandleDeleteDeploymentSuccess(t *testing.T) {
	var gotID uuid.UUID
	stub := &stubDeploymentService{
		deleteFunc: func(_ context.Context, id uuid.UUID) error { gotID = id; return nil },
	}
	id := uuid.New()
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodDelete, "/deployments/"+id.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotID != id {
		t.Errorf("service received id %s, want %s", gotID, id)
	}
}

func TestHandleDeleteDeploymentUnknownReturns404(t *testing.T) {
	stub := &stubDeploymentService{
		deleteFunc: func(context.Context, uuid.UUID) error { return deployment.ErrNotFound },
	}
	rec := doDeploymentReq(t, newDeploymentsTestRouter(stub), http.MethodDelete, "/deployments/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteDeploymentInvalidID(t *testing.T) {
	rec := doDeploymentReq(t, newDeploymentsTestRouter(&stubDeploymentService{}), http.MethodDelete, "/deployments/not-a-uuid", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- Wrong method / unimplemented routes ---------------------------------------

func TestDeploymentRoutesRejectWrongMethod(t *testing.T) {
	stub := &stubDeploymentService{}
	router := newDeploymentsTestRouter(stub)

	tests := []struct{ method, path string }{
		{http.MethodDelete, "/deployments"},
		{http.MethodPost, "/deployments/" + uuid.New().String()},
	}
	for _, tc := range tests {
		rec := doDeploymentReq(t, router, tc.method, tc.path, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}

// Explicit scope guard: Phase 2.1 must not implement scale/restart/rollback.
func TestDeploymentScaleRestartRollbackAreNotImplemented(t *testing.T) {
	stub := &stubDeploymentService{}
	router := newDeploymentsTestRouter(stub)
	id := uuid.New().String()

	for _, path := range []string{
		"/deployments/" + id + "/scale",
		"/deployments/" + id + "/restart",
		"/deployments/" + id + "/rollback",
	} {
		rec := doDeploymentReq(t, router, http.MethodPost, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: status = %d, want %d (not implemented in Phase 2.1)", path, rec.Code, http.StatusNotFound)
		}
	}
}
