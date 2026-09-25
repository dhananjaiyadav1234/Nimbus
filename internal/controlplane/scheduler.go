package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/scheduler"
	"github.com/dhananjaiyadav1234/Nimbus/internal/schedulerapi"
)

// SchedulerService is exactly the scheduling behaviour the HTTP layer
// needs. Handlers depend on this interface — implemented by
// *scheduler.Service — rather than on that concrete type, the same pattern
// NodeService and DeploymentService already establish in this package.
type SchedulerService interface {
	Schedule(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error)
	Placements(ctx context.Context, deploymentID uuid.UUID) ([]scheduler.Placement, error)
}

// handleScheduleDeployment implements POST /deployments/{id}/schedule.
// Deployment creation (POST /deployments) remains persistence-only — this
// is the only route that ever invokes the scheduler; see
// docs/scheduling.md.
func (a *api) handleScheduleDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "deployment id is not a valid UUID"})
		return
	}

	placements, err := a.scheduler.Schedule(r.Context(), id)
	switch {
	case errors.Is(err, scheduler.ErrDeploymentNotFound):
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "deployment not found"})
		return
	case errors.Is(err, scheduler.ErrInsufficientCapacity):
		a.logger.WarnContext(r.Context(), "scheduling failed: insufficient cluster capacity",
			slog.String("deployment_id", id.String()), slog.String("error", err.Error()))
		a.writeJSON(r.Context(), w, http.StatusConflict, errorResponse{Error: "insufficient cluster capacity to schedule every replica"})
		return
	case err != nil:
		a.logger.ErrorContext(r.Context(), "scheduling deployment failed", slog.String("error", err.Error()), slog.String("deployment_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to schedule deployment"})
		return
	}

	a.logger.InfoContext(r.Context(), "deployment scheduled",
		slog.String("deployment_id", id.String()),
		slog.Int("placements", len(placements)),
	)

	a.writeJSON(r.Context(), w, http.StatusOK, toScheduleResponse(id, placements))
}

// handleGetPlacements implements GET /deployments/{id}/placements.
func (a *api) handleGetPlacements(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.writeJSON(r.Context(), w, http.StatusBadRequest, errorResponse{Error: "deployment id is not a valid UUID"})
		return
	}

	placements, err := a.scheduler.Placements(r.Context(), id)
	if errors.Is(err, scheduler.ErrDeploymentNotFound) {
		a.writeJSON(r.Context(), w, http.StatusNotFound, errorResponse{Error: "deployment not found"})
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "getting placements failed", slog.String("error", err.Error()), slog.String("deployment_id", id.String()))
		a.writeJSON(r.Context(), w, http.StatusInternalServerError, errorResponse{Error: "unable to get placements"})
		return
	}

	a.writeJSON(r.Context(), w, http.StatusOK, toPlacementsResponse(id, placements))
}

func toScheduleResponse(deploymentID uuid.UUID, placements []scheduler.Placement) schedulerapi.ScheduleResponse {
	return schedulerapi.ScheduleResponse{
		DeploymentID: deploymentID.String(),
		Placements:   toPlacementDTOs(placements),
	}
}

func toPlacementsResponse(deploymentID uuid.UUID, placements []scheduler.Placement) schedulerapi.PlacementsResponse {
	return schedulerapi.PlacementsResponse{
		DeploymentID: deploymentID.String(),
		Placements:   toPlacementDTOs(placements),
	}
}

func toPlacementDTOs(placements []scheduler.Placement) []schedulerapi.PlacementDTO {
	dtos := make([]schedulerapi.PlacementDTO, len(placements))
	for i, p := range placements {
		dtos[i] = schedulerapi.PlacementDTO{
			ReplicaIndex: p.ReplicaIndex,
			NodeID:       p.NodeID.String(),
			CPU:          p.CPU,
			MemoryBytes:  p.MemoryBytes,
		}
	}
	return dtos
}
