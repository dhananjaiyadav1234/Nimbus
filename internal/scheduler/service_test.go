package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deployment"
)

// stubDeploymentLookup is a DeploymentLookup test double, mirroring
// internal/controlplane's stub*Service pattern.
type stubDeploymentLookup struct {
	getFunc func(ctx context.Context, id uuid.UUID) (deployment.Deployment, error)
}

func (s *stubDeploymentLookup) Get(ctx context.Context, id uuid.UUID) (deployment.Deployment, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, id)
	}
	return deployment.Deployment{}, deployment.ErrNotFound
}

func TestNewServiceRejectsNilDependencies(t *testing.T) {
	if _, err := NewService(nil, &stubDeploymentLookup{}); err == nil {
		t.Error("NewService with a nil repository succeeded, want an error")
	}
	if _, err := NewService(newFakeRepository(), nil); err == nil {
		t.Error("NewService with a nil deployment lookup succeeded, want an error")
	}
}

func TestServiceScheduleUnknownDeploymentReturnsErrDeploymentNotFound(t *testing.T) {
	svc, err := NewService(newFakeRepository(), &stubDeploymentLookup{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.Schedule(context.Background(), uuid.New())
	if !errors.Is(err, ErrDeploymentNotFound) {
		t.Errorf("error = %v, want ErrDeploymentNotFound", err)
	}
}

func TestServicePlacementsUnknownDeploymentReturnsErrDeploymentNotFound(t *testing.T) {
	svc, err := NewService(newFakeRepository(), &stubDeploymentLookup{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.Placements(context.Background(), uuid.New())
	if !errors.Is(err, ErrDeploymentNotFound) {
		t.Errorf("error = %v, want ErrDeploymentNotFound", err)
	}
}

func TestServiceScheduleSuccess(t *testing.T) {
	repo := newFakeRepository()
	node := newNode(4, 4096)
	repo.addNode(node)

	d := deployment.Deployment{ID: uuid.New(), Name: "web", Replicas: 2, CPU: 1, MemoryBytes: 1024}
	repo.knowDeployment(d.ID)

	lookup := &stubDeploymentLookup{getFunc: func(_ context.Context, id uuid.UUID) (deployment.Deployment, error) {
		if id != d.ID {
			return deployment.Deployment{}, deployment.ErrNotFound
		}
		return d, nil
	}}

	svc, err := NewService(repo, lookup)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	placements, err := svc.Schedule(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}
}

// Test 13 (Service-level) — Idempotency: scheduling the same deployment
// twice through the full Service must not duplicate placements.
func TestServiceScheduleIsIdempotent(t *testing.T) {
	repo := newFakeRepository()
	repo.addNode(newNode(4, 4096))

	d := deployment.Deployment{ID: uuid.New(), Name: "web", Replicas: 3, CPU: 1, MemoryBytes: 1024}
	repo.knowDeployment(d.ID)
	lookup := &stubDeploymentLookup{getFunc: func(_ context.Context, _ uuid.UUID) (deployment.Deployment, error) { return d, nil }}

	svc, err := NewService(repo, lookup)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := svc.Schedule(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("first Schedule: %v", err)
	}
	second, err := svc.Schedule(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("second Schedule: %v", err)
	}

	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("first=%d second=%d placements, want 3 and 3 (no duplicates)", len(first), len(second))
	}
	if !placementsEqual(first, second) {
		t.Errorf("second Schedule produced a different result than the first:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestServiceScheduleInsufficientCapacityPropagates(t *testing.T) {
	repo := newFakeRepository()
	repo.addNode(newNode(1, 1024)) // room for exactly 1 replica

	d := deployment.Deployment{ID: uuid.New(), Name: "web", Replicas: 2, CPU: 1, MemoryBytes: 1024}
	repo.knowDeployment(d.ID)
	lookup := &stubDeploymentLookup{getFunc: func(_ context.Context, _ uuid.UUID) (deployment.Deployment, error) { return d, nil }}

	svc, err := NewService(repo, lookup)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.Schedule(context.Background(), d.ID)
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("error = %v, want ErrInsufficientCapacity", err)
	}

	// Atomicity through the full Service: a failed schedule must leave
	// zero placements behind, not even for the one replica that could
	// have fit.
	placements, err := repo.ListByDeployment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("ListByDeployment: %v", err)
	}
	if len(placements) != 0 {
		t.Errorf("found %d placements after a failed Schedule, want 0", len(placements))
	}
}

func TestServicePlacementsReturnsExisting(t *testing.T) {
	repo := newFakeRepository()
	repo.addNode(newNode(4, 4096))

	d := deployment.Deployment{ID: uuid.New(), Name: "web", Replicas: 1, CPU: 1, MemoryBytes: 1024}
	repo.knowDeployment(d.ID)
	lookup := &stubDeploymentLookup{getFunc: func(_ context.Context, _ uuid.UUID) (deployment.Deployment, error) { return d, nil }}

	svc, err := NewService(repo, lookup)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.Schedule(context.Background(), d.ID); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	placements, err := svc.Placements(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("Placements: %v", err)
	}
	if len(placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(placements))
	}
}

func TestServicePlacementsEmptyBeforeScheduling(t *testing.T) {
	repo := newFakeRepository()
	d := deployment.Deployment{ID: uuid.New(), Name: "web", Replicas: 1, CPU: 1, MemoryBytes: 1024}
	repo.knowDeployment(d.ID)
	lookup := &stubDeploymentLookup{getFunc: func(_ context.Context, _ uuid.UUID) (deployment.Deployment, error) { return d, nil }}

	svc, err := NewService(repo, lookup)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	placements, err := svc.Placements(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("Placements: %v", err)
	}
	if len(placements) != 0 {
		t.Errorf("got %d placements before any Schedule call, want 0", len(placements))
	}
}
