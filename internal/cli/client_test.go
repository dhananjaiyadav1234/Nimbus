package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
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
