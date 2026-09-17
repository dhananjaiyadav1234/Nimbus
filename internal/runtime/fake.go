package runtime

import (
	"context"
	"fmt"
	"sync"
)

// FakeRuntime is an in-memory ContainerRuntime, for tests that exercise
// code depending on ContainerRuntime without a real Docker daemon —
// mirroring internal/cluster's fakeRepository pattern, except exported
// (not a _test.go file) since, unlike that package-private fake, callers
// outside this package (Phase 2.2's scheduler tests, for instance) are
// expected to need it too, the same reason internal/database/dbtest is a
// regular importable package rather than test-only.
type FakeRuntime struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	nextID     int

	// CreateErr, if set, is returned by every CreateContainer call — for
	// tests exercising the caller's handling of a runtime that is
	// unavailable or misbehaving.
	CreateErr error
}

type fakeContainer struct {
	spec    ContainerSpec
	running bool
	removed bool
}

// NewFakeRuntime returns an empty FakeRuntime.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{containers: map[string]*fakeContainer{}}
}

func (f *FakeRuntime) CreateContainer(_ context.Context, spec ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.CreateErr != nil {
		return "", f.CreateErr
	}

	f.nextID++
	id := fmt.Sprintf("fake-%d", f.nextID)
	f.containers[id] = &fakeContainer{spec: spec}
	return id, nil
}

func (f *FakeRuntime) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.containers[id]
	if !ok || c.removed {
		return ErrContainerNotFound
	}
	c.running = true
	return nil
}

func (f *FakeRuntime) StopContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.containers[id]
	if !ok || c.removed {
		return ErrContainerNotFound
	}
	c.running = false
	return nil
}

func (f *FakeRuntime) RemoveContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.containers[id]
	if !ok || c.removed {
		return nil // idempotent — see ContainerRuntime.RemoveContainer
	}
	c.removed = true
	return nil
}

func (f *FakeRuntime) InspectContainer(_ context.Context, id string) (ContainerStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.containers[id]
	if !ok || c.removed {
		return ContainerStatus{}, ErrContainerNotFound
	}

	state := "created"
	if c.running {
		state = "running"
	}
	return ContainerStatus{
		ID:      id,
		Name:    c.spec.Name,
		Image:   c.spec.Image,
		State:   state,
		Running: c.running,
	}, nil
}
