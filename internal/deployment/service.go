package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service enforces Nimbus's workload rules on top of a Repository. It is
// the only place those rules live — HTTP handlers translate wire formats to
// and from these methods and never touch SQL directly, mirroring
// internal/cluster.Service.
type Service struct {
	repo Repository

	// now stands in for time.Now so tests can control timestamps without
	// relying on wall-clock timing, the same pattern cluster.Service uses.
	now func() time.Time
}

// NewService builds a Service.
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("deployment: repository is required")
	}
	return &Service{repo: repo, now: time.Now}, nil
}

// Create validates in and persists it as a new Deployment. A single
// INSERT is already atomic by itself — there is no second dependent write
// in Phase 2.1 (no scheduling assignment, no container record) — so no
// explicit transaction is needed here; see docs/workloads.md.
//
// Name uniqueness is enforced by PostgreSQL's UNIQUE constraint, not an
// application-level pre-check, so this is correct even when two requests
// for the same name race: exactly one Create succeeds and the other
// receives ErrAlreadyExists, never two rows.
func (s *Service) Create(ctx context.Context, in CreateInput) (Deployment, error) {
	if err := in.Validate(); err != nil {
		return Deployment{}, err
	}

	now := s.now()
	d := Deployment{
		ID:          uuid.New(),
		Name:        in.Name,
		Image:       in.Image,
		Replicas:    in.Replicas,
		CPU:         in.CPU,
		MemoryBytes: in.MemoryBytes,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	saved, err := s.repo.Create(ctx, d)
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return Deployment{}, ErrAlreadyExists
		}
		return Deployment{}, fmt.Errorf("deployment: creating: %w", err)
	}
	return saved, nil
}

// Get returns a single deployment by ID, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Deployment, error) {
	d, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("deployment: getting: %w", err)
	}
	return d, nil
}

// GetByName returns a single deployment by name, or ErrNotFound.
func (s *Service) GetByName(ctx context.Context, name string) (Deployment, error) {
	d, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("deployment: getting %q: %w", name, err)
	}
	return d, nil
}

// List returns every known deployment, ordered by name ascending. It always
// queries PostgreSQL directly, never a cached list, so a Control Plane
// restart never loses visibility into desired state.
func (s *Service) List(ctx context.Context) ([]Deployment, error) {
	deployments, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("deployment: listing: %w", err)
	}
	return deployments, nil
}

// Delete removes a deployment's desired state from PostgreSQL. It does
// NOT stop, remove, or otherwise touch any container — Phase 2.1 has no
// scheduler and no record of which node, if any, is running a deployment's
// replicas. See docs/workloads.md's "Deployment lifecycle" section.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("deployment: deleting: %w", err)
	}
	return nil
}
