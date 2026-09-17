package nodeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

// requestTimeout bounds a single HTTP call to the Control Plane. It is
// deliberately shorter than the heartbeat interval floor so a stuck call
// cannot itself cause a missed heartbeat cycle.
const requestTimeout = 5 * time.Second

// ErrNodeNotFound is returned by Heartbeat when the Control Plane responds
// 404 — its membership record for this node no longer exists (for example
// after the database was reset), and the agent must register again.
var ErrNodeNotFound = errors.New("nodeagent: control plane does not know this node")

// Client is a small HTTP client for the Control Plane's node API. It knows
// nothing about retries or identity; that behaviour lives in Agent, one
// layer up, so Client stays a thin, easily-testable translation between Go
// values and the clusterapi wire contract.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client. baseURL is the Control Plane's origin, e.g.
// "http://localhost:8080".
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// Register calls POST /nodes/register.
func (c *Client) Register(ctx context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error) {
	var resp clusterapi.RegisterResponse
	if err := c.postJSON(ctx, "/nodes/register", req, &resp); err != nil {
		return clusterapi.RegisterResponse{}, err
	}
	return resp, nil
}

// Heartbeat calls POST /nodes/{id}/heartbeat. It returns ErrNodeNotFound
// (wrapped, so errors.Is still matches) if the Control Plane responds 404.
func (c *Client) Heartbeat(ctx context.Context, nodeID uuid.UUID, req clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error) {
	var resp clusterapi.HeartbeatResponse
	err := c.postJSON(ctx, "/nodes/"+nodeID.String()+"/heartbeat", req, &resp)

	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
		return clusterapi.HeartbeatResponse{}, fmt.Errorf("%w: %s", ErrNodeNotFound, statusErr.Message)
	}
	return resp, err
}

// StatusError is returned by Client methods when the Control Plane answers
// with a non-2xx status. Message is the server's own {"error": "..."} body —
// never a raw HTTP body dump — so callers get a clean, user-safe string.
type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("control plane responded %d: %s", e.StatusCode, e.Message)
}

// postJSON sends body as a JSON POST to path and decodes a JSON response
// into out. A non-2xx response is reported as a *StatusError.
func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("nodeagent: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("nodeagent: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nodeagent: calling control plane: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody) // best-effort; empty Message if undecodable
		return &StatusError{StatusCode: resp.StatusCode, Message: errBody.Error}
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("nodeagent: decoding response: %w", err)
		}
	}
	return nil
}
