// Package cli implements the `nimbus` command-line client: HTTP calls
// against the Control Plane's API, and formatting the results for a
// terminal. It never talks to PostgreSQL directly — see
// docs/cluster-membership.md for why that matters architecturally.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

// requestTimeout bounds a single call to the Control Plane.
const requestTimeout = 5 * time.Second

// StatusError is returned when the Control Plane answers with a non-2xx
// status. Message is the server's own {"error": "..."} text.
type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("control plane responded %d: %s", e.StatusCode, e.Message)
}

// ControlPlaneClient is a minimal HTTP client for the read side of the
// Control Plane's node API — everything the CLI needs and nothing more.
type ControlPlaneClient struct {
	baseURL string
	http    *http.Client
}

// NewControlPlaneClient builds a ControlPlaneClient. baseURL is the Control
// Plane's origin, e.g. "http://localhost:8080".
func NewControlPlaneClient(baseURL string) *ControlPlaneClient {
	return &ControlPlaneClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// ListNodes calls GET /nodes.
func (c *ControlPlaneClient) ListNodes(ctx context.Context) (clusterapi.ListNodesResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/nodes", nil)
	if err != nil {
		return clusterapi.ListNodesResponse{}, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return clusterapi.ListNodesResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body) // best-effort
		return clusterapi.ListNodesResponse{}, &StatusError{StatusCode: resp.StatusCode, Message: body.Error}
	}

	var out clusterapi.ListNodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return clusterapi.ListNodesResponse{}, fmt.Errorf("decoding response: %w", err)
	}
	return out, nil
}

// FriendlyError converts err into a short, terminal-appropriate message: a
// connection-level failure (Control Plane unreachable) becomes a fixed,
// non-technical sentence rather than a raw dial error and stack; a
// Control-Plane-reported error keeps its own text; anything else falls back
// to a generic, still-safe message. Nothing this returns ever includes a Go
// error chain or a stack trace.
func FriendlyError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "unable to connect to Nimbus control plane"
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		if statusErr.Message != "" {
			return statusErr.Message
		}
		return fmt.Sprintf("control plane returned an unexpected error (status %d)", statusErr.StatusCode)
	}

	return "unexpected error communicating with the control plane"
}
