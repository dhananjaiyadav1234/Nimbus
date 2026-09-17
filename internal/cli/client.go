// Package cli implements the `nimbus` command-line client: HTTP calls
// against the Control Plane's API, and formatting the results for a
// terminal. It never talks to PostgreSQL directly — see
// docs/cluster-membership.md for why that matters architecturally.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
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
	var out clusterapi.ListNodesResponse
	err := c.do(ctx, http.MethodGet, "/nodes", nil, &out)
	return out, err
}

// CreateDeployment calls POST /deployments with manifest as the request
// body — the same struct the CLI parsed from a YAML file (see
// deploymentapi's own doc comment for why the wire format and the YAML
// file format are one type).
func (c *ControlPlaneClient) CreateDeployment(ctx context.Context, manifest deploymentapi.Manifest) (deploymentapi.DeploymentDTO, error) {
	var out deploymentapi.DeploymentDTO
	err := c.do(ctx, http.MethodPost, "/deployments", manifest, &out)
	return out, err
}

// ListDeployments calls GET /deployments.
func (c *ControlPlaneClient) ListDeployments(ctx context.Context) (deploymentapi.ListDeploymentsResponse, error) {
	var out deploymentapi.ListDeploymentsResponse
	err := c.do(ctx, http.MethodGet, "/deployments", nil, &out)
	return out, err
}

// do sends method/path to the Control Plane, JSON-encoding body if
// non-nil, and JSON-decodes a successful response into out (if out is
// non-nil). A non-2xx response is reported as a *StatusError carrying the
// Control Plane's own {"error": "..."} text.
func (c *ControlPlaneClient) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	}
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody) // best-effort
		return &StatusError{StatusCode: resp.StatusCode, Message: errBody.Error}
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
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
