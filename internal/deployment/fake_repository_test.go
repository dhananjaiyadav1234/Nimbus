package deployment

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
)

// fakeRepository is an in-memory Repository used by unit tests, mirroring
// internal/cluster's fakeRepository. Create enforces name uniqueness itself
// (an application-level check) purely to exercise Service's handling of
// ErrAlreadyExists in unit tests — the real guarantee against concurrent
// duplicate creation comes from PostgreSQL's UNIQUE constraint, verified
// separately by the real-database concurrency test in repository_test.go.
type fakeRepository struct {
	mu          sync.Mutex
	deployments map[uuid.UUID]Deployment
	createCalls int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{deployments: map[uuid.UUID]Deployment{}}
}

func (f *fakeRepository) Create(_ context.Context, d Deployment) (Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.createCalls++
	for _, existing := range f.deployments {
		if existing.Name == d.Name {
			return Deployment{}, ErrAlreadyExists
		}
	}
	f.deployments[d.ID] = d
	return d, nil
}

func (f *fakeRepository) GetByID(_ context.Context, id uuid.UUID) (Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	d, ok := f.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	return d, nil
}

func (f *fakeRepository) GetByName(_ context.Context, name string) (Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, d := range f.deployments {
		if d.Name == name {
			return d, nil
		}
	}
	return Deployment{}, ErrNotFound
}

func (f *fakeRepository) List(_ context.Context) ([]Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	deployments := make([]Deployment, 0, len(f.deployments))
	for _, d := range f.deployments {
		deployments = append(deployments, d)
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].Name < deployments[j].Name })
	return deployments, nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.deployments[id]; !ok {
		return ErrNotFound
	}
	delete(f.deployments, id)
	return nil
}
