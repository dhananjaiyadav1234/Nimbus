package nodeagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

func TestClientRegisterSuccess(t *testing.T) {
	var gotReq clusterapi.RegisterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/nodes/register" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(clusterapi.RegisterResponse{
			NodeID: "abc-123", Status: "Ready", HeartbeatIntervalSeconds: 10,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.Register(context.Background(), clusterapi.RegisterRequest{Hostname: "worker-01"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.NodeID != "abc-123" {
		t.Errorf("NodeID = %q, want %q", resp.NodeID, "abc-123")
	}
	if gotReq.Hostname != "worker-01" {
		t.Errorf("server received Hostname = %q, want %q", gotReq.Hostname, "worker-01")
	}
}

func TestClientRegisterSurfacesValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "hostname must not be empty"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Register(context.Background(), clusterapi.RegisterRequest{})
	if err == nil {
		t.Fatal("Register succeeded, want an error")
	}

	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusBadRequest)
	}
	if statusErr.Message != "hostname must not be empty" {
		t.Errorf("Message = %q, want the server's error text", statusErr.Message)
	}
}

func TestClientHeartbeatSuccess(t *testing.T) {
	nodeID := uuid.New()
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(clusterapi.HeartbeatResponse{Status: "Ready"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.Heartbeat(context.Background(), nodeID, clusterapi.HeartbeatRequest{})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if resp.Status != "Ready" {
		t.Errorf("Status = %q, want %q", resp.Status, "Ready")
	}
	if want := "/nodes/" + nodeID.String() + "/heartbeat"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

func TestClientHeartbeatUnknownNodeReturnsErrNodeNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "node not found"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Heartbeat(context.Background(), uuid.New(), clusterapi.HeartbeatRequest{})
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("error = %v, want ErrNodeNotFound", err)
	}
}

func TestClientSurfacesConnectionFailureWithoutPanicking(t *testing.T) {
	client := NewClient("http://127.0.0.1:1") // nothing listens here
	_, err := client.Register(context.Background(), clusterapi.RegisterRequest{Hostname: "x"})
	if err == nil {
		t.Fatal("Register against an unreachable server succeeded, want an error")
	}
}
