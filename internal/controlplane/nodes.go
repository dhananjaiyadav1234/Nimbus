package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/cluster"
	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

// NodeService is exactly the cluster-membership behaviour the HTTP layer
// needs. Handlers depend on this interface — implemented by *cluster.Service
// — rather than on that concrete type, the same way handleReady depends on
// health.Checker rather than a database handle: it lets these handlers be
// tested with a stub and no PostgreSQL.
type NodeService interface {
	Register(ctx context.Context, in cluster.RegisterInput) (cluster.RegisterResult, error)
	Heartbeat(ctx context.Context, id uuid.UUID) (cluster.HeartbeatResult, error)
	Get(ctx context.Context, id uuid.UUID) (cluster.Node, error)
	List(ctx context.Context) ([]cluster.Node, error)
}

// handleRegisterNode implements POST /nodes/register. See
// docs/cluster-membership.md for the full registration contract, including
// why a successful registration assigns StatusReady directly.
func (a *api) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	var req clusterapi.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "request body is not valid JSON"})
		return
	}

	in := cluster.RegisterInput{
		Hostname:            req.Hostname,
		OS:                  req.OS,
		Architecture:        req.Architecture,
		CPUCapacity:         req.CPUCapacity,
		MemoryCapacityBytes: req.MemoryCapacityBytes,
		AgentVersion:        req.AgentVersion,
	}
	if req.NodeID != "" {
		id, err := uuid.Parse(req.NodeID)
		if err != nil {
			a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "node_id is not a valid UUID"})
			return
		}
		in.NodeID = id
	}

	result, err := a.nodes.Register(r.Context(), in)
	if err != nil {
		var verr *cluster.ValidationError
		if errors.As(err, &verr) {
			a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: verr.Error()})
			return
		}
		a.logger.ErrorContext(r.Context(), "node registration failed", slog.String("error", err.Error()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to register node"})
		return
	}

	a.logger.InfoContext(r.Context(), "node registered",
		slog.String("node_id", result.Node.ID.String()),
		slog.String("hostname", result.Node.Hostname),
	)

	a.writeJSON(r.Context(), w, http.StatusOK, clusterapi.RegisterResponse{
		NodeID:                   result.Node.ID.String(),
		Status:                   string(result.Node.Status),
		HeartbeatIntervalSeconds: int(result.HeartbeatInterval / time.Second),
	})
}

// handleHeartbeat implements POST /nodes/{id}/heartbeat. A missing or empty
// body is accepted — the request's only mandatory content is the node ID in
// the URL — since the optional Timestamp field is informational only; the
// Control Plane's own receipt time is always what decides liveness.
func (a *api) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "node id is not a valid UUID"})
		return
	}

	var req clusterapi.HeartbeatRequest
	if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "request body is not valid JSON"})
		return
	}
	// req.Timestamp, if present, is deliberately unused for the liveness
	// decision below — see clusterapi.HeartbeatRequest.

	result, err := a.nodes.Heartbeat(r.Context(), id)
	if errors.Is(err, cluster.ErrNotFound) {
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "node not found"})
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "heartbeat failed", slog.String("error", err.Error()), slog.String("node_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to record heartbeat"})
		return
	}

	// Every ordinary heartbeat is deliberately silent — only the recovery
	// transition is worth an INFO log, per docs/cluster-membership.md's
	// logging policy.
	if result.Recovered {
		a.logger.InfoContext(r.Context(), "node became Ready",
			slog.String("node_id", result.Node.ID.String()),
			slog.String("hostname", result.Node.Hostname),
		)
	}

	a.writeJSON(r.Context(), w, http.StatusOK, clusterapi.HeartbeatResponse{Status: string(result.Node.Status)})
}

// handleGetNode implements GET /nodes/{id}.
func (a *api) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "node id is not a valid UUID"})
		return
	}

	node, err := a.nodes.Get(r.Context(), id)
	if errors.Is(err, cluster.ErrNotFound) {
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "node not found"})
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "getting node failed", slog.String("error", err.Error()), slog.String("node_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to get node"})
		return
	}

	a.writeJSON(r.Context(), w, http.StatusOK, toNodeDTO(node))
}

// handleListNodes implements GET /nodes. It always queries PostgreSQL
// through Service.List — never a cached in-memory list — so membership is
// accurate immediately after a Control Plane restart.
func (a *api) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.nodes.List(r.Context())
	if err != nil {
		a.logger.ErrorContext(r.Context(), "listing nodes failed", slog.String("error", err.Error()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to list nodes"})
		return
	}

	dtos := make([]clusterapi.NodeDTO, len(nodes))
	for i, node := range nodes {
		dtos[i] = toNodeDTO(node)
	}
	a.writeJSON(r.Context(), w, http.StatusOK, clusterapi.ListNodesResponse{Nodes: dtos})
}

func toNodeDTO(n cluster.Node) clusterapi.NodeDTO {
	return clusterapi.NodeDTO{
		ID:                  n.ID.String(),
		Hostname:            n.Hostname,
		Status:              string(n.Status),
		OS:                  n.OS,
		Architecture:        n.Architecture,
		CPUCapacity:         n.CPUCapacity,
		MemoryCapacityBytes: n.MemoryCapacityBytes,
		AgentVersion:        n.AgentVersion,
		LastHeartbeatAt:     n.LastHeartbeatAt,
		RegisteredAt:        n.RegisteredAt,
		UpdatedAt:           n.UpdatedAt,
	}
}
