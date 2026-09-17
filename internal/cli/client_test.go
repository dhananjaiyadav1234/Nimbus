package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
)

func TestControlPlaneClientListNodesSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/nodes" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(clusterapi.ListNodesResponse{
			Nodes: []clusterapi.NodeDTO{{Hostname: "node-a", Status: "Ready"}},
		})
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	resp, err := client.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Hostname != "node-a" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestControlPlaneClientListNodesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "unable to list nodes"})
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	_, err := client.ListNodes(context.Background())
	if err == nil {
		t.Fatal("ListNodes succeeded, want an error")
	}
	if got := FriendlyError(err); got != "unable to list nodes" {
		t.Errorf("FriendlyError(...) = %q, want the server's message", got)
	}
}

// A malformed (non-JSON, or truncated) 2xx response must be reported as an
// ordinary error — never panic the CLI.
func TestControlPlaneClientListNodesMalformedResponseBodyDoesNotPanic(t *testing.T) {
	tests := map[string]string{
		"not json at all":      "<html>not json</html>",
		"truncated json":       `{"nodes": [{"hostname": "node-a"`,
		"empty body":           "",
		"json but wrong shape": `"just a string, not an object"`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			}))
			defer server.Close()

			client := NewControlPlaneClient(server.URL)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ListNodes panicked on malformed response %q: %v", body, r)
				}
			}()

			_, err := client.ListNodes(context.Background())
			if err == nil {
				t.Errorf("ListNodes succeeded on malformed body %q, want an error", body)
			}
		})
	}
}

func TestFriendlyErrorForUnreachableControlPlane(t *testing.T) {
	client := NewControlPlaneClient("http://127.0.0.1:1") // nothing listens here
	_, err := client.ListNodes(context.Background())
	if err == nil {
		t.Fatal("ListNodes against an unreachable server succeeded, want an error")
	}

	got := FriendlyError(err)
	if got != "unable to connect to Nimbus control plane" {
		t.Errorf("FriendlyError(...) = %q, want the fixed connection-failure message", got)
	}
	// The whole point of FriendlyError is to never leak raw dial detail.
	if strings.Contains(got, "dial") || strings.Contains(got, "connect:") || strings.Contains(got, "127.0.0.1") {
		t.Errorf("FriendlyError(...) leaked low-level detail: %q", got)
	}
}

func TestControlPlaneClientCreateDeploymentSuccess(t *testing.T) {
	var gotBody deploymentapi.Manifest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/deployments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deploymentapi.DeploymentDTO{ID: "abc-123", Name: gotBody.Metadata.Name})
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	manifest := deploymentapi.Manifest{
		APIVersion: deploymentapi.SupportedAPIVersion,
		Kind:       deploymentapi.DeploymentKind,
		Metadata:   deploymentapi.Metadata{Name: "web"},
		Spec: deploymentapi.Spec{
			Image: "nginx:latest", Replicas: 2,
			Resources: &deploymentapi.Resources{CPU: 1, Memory: "512Mi"},
		},
	}

	dto, err := client.CreateDeployment(context.Background(), manifest)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if dto.ID != "abc-123" {
		t.Errorf("ID = %q, want %q", dto.ID, "abc-123")
	}
	if gotBody.Metadata.Name != "web" {
		t.Errorf("server received name %q, want %q", gotBody.Metadata.Name, "web")
	}
}

func TestControlPlaneClientCreateDeploymentConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "a deployment named \"web\" already exists"})
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	_, err := client.CreateDeployment(context.Background(), deploymentapi.Manifest{})
	if err == nil {
		t.Fatal("CreateDeployment succeeded, want an error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusConflict {
		t.Errorf("error = %v, want a *StatusError with status 409", err)
	}
}

func TestControlPlaneClientListDeploymentsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/deployments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deploymentapi.ListDeploymentsResponse{
			Deployments: []deploymentapi.DeploymentDTO{{Name: "web"}},
		})
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	resp, err := client.ListDeployments(context.Background())
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(resp.Deployments) != 1 || resp.Deployments[0].Name != "web" {
		t.Errorf("unexpected response: %+v", resp)
	}
}
