package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
)

// DeploymentLookup is exactly the deployment-reading behaviour Service
// needs. Implemented by *deployment.Service; Service depends on this
// narrow interface rather than that concrete type, the same pattern
// internal/controlplane's NodeService/DeploymentService interfaces use —
// it lets Service be tested with a stub and no PostgreSQL, and makes
// explicit that scheduling only ever reads a deployment's desired state,
// never writes it.
type DeploymentLookup interface {
	Get(ctx context.Context, id uuid.UUID) (deployment.Deployment, error)
}

// Service enforces Nimbus's scheduling rules on top of a Repository. It is
// the only place those rules live — HTTP handlers translate wire formats to
// and from these methods and never touch SQL or the scheduling algorithm
// directly, mirroring internal/cluster.Service and
// internal/deployment.Service.
//
// Service operates entirely on domain models (deployment.Deployment,
// Placement) and knows nothing of HTTP, JSON, the CLI, Docker, or
// internal/runtime — see this package's own doc comment for the full
// boundary.
type Service struct {
	repo        Repository
	deployments DeploymentLookup

	// now stands in for time.Now so tests can control placement timestamps
	// without relying on wall-clock timing — the same pattern
	// cluster.Service and deployment.Service use.
	now func() time.Time
}

// NewService builds a Service.
func NewService(repo Repository, deployments DeploymentLookup) (*Service, error) {
	if repo == nil {
		return nil, errors.New("scheduler: repository is required")
	}
	if deployments == nil {
		return nil, errors.New("scheduler: deployment lookup is required")
	}
	return &Service{repo: repo, deployments: deployments, now: time.Now}, nil
}

// Schedule computes and persists placements for every replica of
// deploymentID that does not already have one, using the deterministic
// best-fit algorithm documented on decidePlacements and
// docs/scheduling.md, and returns the deployment's complete placement set
// (existing plus newly created) ordered by replica index ascending.
//
// Schedule is idempotent: calling it again for a deployment whose replicas
// are all already placed succeeds and returns the existing placements
// unchanged, creating nothing new — see Repository.Schedule for the
// transactional guarantee this and every other correctness property here
// depends on.
//
// Returns ErrDeploymentNotFound if deploymentID does not exist, or
// ErrInsufficientCapacity if the cluster's Ready nodes cannot currently fit
// every still-missing replica — in the latter case, no partial result is
// ever persisted.
func (s *Service) Schedule(ctx context.Context, deploymentID uuid.UUID) ([]Placement, error) {
	d, err := s.deployments.Get(ctx, deploymentID)
	if errors.Is(err, deployment.ErrNotFound) {
		return nil, ErrDeploymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scheduler: loading deployment %s: %w", deploymentID, err)
	}

	decide := newDecideFunc(d, s.now())

	placements, err := s.repo.Schedule(ctx, deploymentID, decide)
	if err != nil {
		// ErrDeploymentNotFound here means the deployment was deleted
		// between the Get above and the transaction's own lock attempt —
		// a genuine, if narrow, race the caller should see the same way as
		// the more common case above. ErrInsufficientCapacity and any
		// other error pass through unchanged.
		return nil, err
	}
	return placements, nil
}

// Placements returns deploymentID's current placement set, ordered by
// replica index ascending — empty, not an error, if scheduling has not run
// yet or has not placed every replica. Returns ErrDeploymentNotFound if
// deploymentID does not exist.
func (s *Service) Placements(ctx context.Context, deploymentID uuid.UUID) ([]Placement, error) {
	if _, err := s.deployments.Get(ctx, deploymentID); err != nil {
		if errors.Is(err, deployment.ErrNotFound) {
			return nil, ErrDeploymentNotFound
		}
		return nil, fmt.Errorf("scheduler: loading deployment %s: %w", deploymentID, err)
	}

	placements, err := s.repo.ListByDeployment(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("scheduler: listing placements for deployment %s: %w", deploymentID, err)
	}
	return placements, nil
}
