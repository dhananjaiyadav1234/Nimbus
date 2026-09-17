package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
)

// maxDeploymentBodyBytes bounds a POST /deployments request body. A
// Deployment manifest is a handful of short fields — there is no reason to
// accept anything close to this limit, which exists purely to stop an
// oversized or malicious body from being read into memory at all; ordinary
// manifests are a few hundred bytes.
const maxDeploymentBodyBytes = 64 * 1024

// DeploymentService is exactly the workload behaviour the HTTP layer needs.
// Handlers depend on this interface — implemented by *deployment.Service —
// rather than on that concrete type, the same pattern NodeService and
// health.Checker already establish in this package.
type DeploymentService interface {
	Create(ctx context.Context, in deployment.CreateInput) (deployment.Deployment, error)
	Get(ctx context.Context, id uuid.UUID) (deployment.Deployment, error)
	List(ctx context.Context) ([]deployment.Deployment, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// handleCreateDeployment implements POST /deployments. The request body is
// a deploymentapi.Manifest (see that package's doc comment for why the
// wire format doubles as the CLI's YAML file format). Both the manifest
// envelope (apiVersion/kind) and the deployment's own fields are validated
// here, server-side, regardless of what a client already checked — the
// Control Plane is the single source of truth for those rules.
func (a *api) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDeploymentBodyBytes)

	var m deploymentapi.Manifest
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "request body is not valid JSON"})
		return
	}

	if err := m.ValidateEnvelope(); err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	// ValidateEnvelope already guarantees m.Spec.Resources is non-nil.
	memoryBytes, err := deployment.ParseMemory(m.Spec.Resources.Memory)
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	in := deployment.CreateInput{
		Name:        m.Metadata.Name,
		Image:       m.Spec.Image,
		Replicas:    m.Spec.Replicas,
		CPU:         m.Spec.Resources.CPU,
		MemoryBytes: memoryBytes,
	}

	d, err := a.deployments.Create(r.Context(), in)
	if err != nil {
		var verr *deployment.ValidationError
		switch {
		case errors.As(err, &verr):
			a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: verr.Error()})
		case errors.Is(err, deployment.ErrAlreadyExists):
			a.writeJSON(r.Context(), w, http.StatusConflict, errorResponse{Error: "a deployment named \"" + in.Name + "\" already exists"})
		default:
			a.logger.ErrorContext(r.Context(), "creating deployment failed", slog.String("error", err.Error()))
			a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to create deployment"})
		}
		return
	}

	a.logger.InfoContext(r.Context(), "deployment created",
		slog.String("deployment_id", d.ID.String()),
		slog.String("deployment_name", d.Name),
	)

	a.writeJSON(r.Context(), w, http.StatusCreated, toDeploymentDTO(d))
}

// handleGetDeployment implements GET /deployments/{id}.
func (a *api) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "deployment id is not a valid UUID"})
		return
	}

	d, err := a.deployments.Get(r.Context(), id)
	if errors.Is(err, deployment.ErrNotFound) {
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "deployment not found"})
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "getting deployment failed", slog.String("error", err.Error()), slog.String("deployment_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to get deployment"})
		return
	}

	a.writeJSON(r.Context(), w, http.StatusOK, toDeploymentDTO(d))
}

// handleListDeployments implements GET /deployments. It always queries
// PostgreSQL through Service.List — never a cached list — so a Control
// Plane restart never loses visibility into desired state.
func (a *api) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	deployments, err := a.deployments.List(r.Context())
	if err != nil {
		a.logger.ErrorContext(r.Context(), "listing deployments failed", slog.String("error", err.Error()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to list deployments"})
		return
	}

	dtos := make([]deploymentapi.DeploymentDTO, len(deployments))
	for i, d := range deployments {
		dtos[i] = toDeploymentDTO(d)
	}
	a.writeJSON(r.Context(), w, http.StatusOK, deploymentapi.ListDeploymentsResponse{Deployments: dtos})
}

// handleDeleteDeployment implements DELETE /deployments/{id}. This removes
// only the desired-state row from PostgreSQL — Phase 2.1 has no scheduler
// and therefore no record of which node, if any, is running a container for
// this deployment, so there is nothing else to clean up yet. See
// docs/workloads.md's "Deployment lifecycle" section.
func (a *api) handleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "deployment id is not a valid UUID"})
		return
	}

	err = a.deployments.Delete(r.Context(), id)
	if errors.Is(err, deployment.ErrNotFound) {
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "deployment not found"})
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "deleting deployment failed", slog.String("error", err.Error()), slog.String("deployment_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to delete deployment"})
		return
	}

	a.logger.InfoContext(r.Context(), "deployment deleted", slog.String("deployment_id", id.String()))
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusNoContent)
}

func toDeploymentDTO(d deployment.Deployment) deploymentapi.DeploymentDTO {
	return deploymentapi.DeploymentDTO{
		ID:          d.ID.String(),
		Name:        d.Name,
		Image:       d.Image,
		Replicas:    d.Replicas,
		CPU:         d.CPU,
		MemoryBytes: d.MemoryBytes,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}
