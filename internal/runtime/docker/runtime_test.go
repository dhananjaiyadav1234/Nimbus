package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/dhananjaiyadav1234/Nimbus/internal/runtime"
)

// These are integration tests: they run against a real Docker Engine —
// exercising the actual Docker Engine API, not a fake — and skip cleanly,
// with a clear reason, when no Docker daemon is reachable, the same
// contract internal/database/dbtest applies for PostgreSQL. Run them with:
//
//	go test ./internal/runtime/docker/...
//
// See docs/workloads.md for prerequisites (Docker must be running; the
// images below must be pullable, or already present locally).
const (
	testImageRunsForever = "nginx:latest"   // long-running default CMD
	testImageExitsAtOnce = "busybox:latest" // used where "exists but not running" suffices
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()

	rt, err := New()
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	t.Cleanup(func() { rt.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.Ping(ctx); err != nil {
		t.Skipf("skipping Docker integration test: Docker Engine is not reachable (%v) — start Docker Desktop/dockerd and ensure the referenced images are pullable", err)
	}

	return rt
}

// uniqueName gives each test its own container name so parallel or
// re-run test invocations never collide with a leftover container from a
// previous run.
func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("nimbus-test-%s-%d", t.Name(), time.Now().UnixNano())
}

func cleanupContainer(t *testing.T, rt *Runtime, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rt.StopContainer(ctx, id)
		_ = rt.RemoveContainer(ctx, id)
	})
}

func TestDockerRuntimeFullLifecycle(t *testing.T) {
	rt := newTestRuntime(t)
	ctx := context.Background()

	spec := runtime.ContainerSpec{
		Name:        uniqueName(t),
		Image:       testImageRunsForever,
		CPU:         1,
		MemoryBytes: 64 * 1024 * 1024,
		Labels:      runtime.ContainerLabels("test-deployment-id", "test-deployment"),
	}

	// Create -> Inspect (not running)
	id, err := rt.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if id == "" {
		t.Fatal("CreateContainer returned an empty ID")
	}
	cleanupContainer(t, rt, id)

	status, err := rt.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer (after create): %v", err)
	}
	if status.Running {
		t.Error("Running = true immediately after create, want false")
	}
	if status.Name != spec.Name {
		t.Errorf("Name = %q, want %q", status.Name, spec.Name)
	}
	if status.Image != spec.Image {
		t.Errorf("Image = %q, want %q", status.Image, spec.Image)
	}

	// Start -> Inspect (running)
	if err := rt.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	status, err = rt.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer (after start): %v", err)
	}
	if !status.Running {
		t.Errorf("Running = false after StartContainer, want true (state: %s)", status.State)
	}

	// Stop -> Inspect (not running)
	if err := rt.StopContainer(ctx, id); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	status, err = rt.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer (after stop): %v", err)
	}
	if status.Running {
		t.Error("Running = true after StopContainer, want false")
	}

	// Remove -> Inspect => not found
	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
	if _, err := rt.InspectContainer(ctx, id); !errors.Is(err, runtime.ErrContainerNotFound) {
		t.Errorf("InspectContainer after remove: error = %v, want ErrContainerNotFound", err)
	}
}

func TestDockerRuntimeAppliesNimbusLabels(t *testing.T) {
	rt := newTestRuntime(t)
	ctx := context.Background()

	spec := runtime.ContainerSpec{
		Name:        uniqueName(t),
		Image:       testImageExitsAtOnce,
		CPU:         1,
		MemoryBytes: 32 * 1024 * 1024,
		Labels:      runtime.ContainerLabels("abc-123", "web"),
	}

	id, err := rt.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	cleanupContainer(t, rt, id)

	// Verified via a raw Docker inspect call (bypassing our own normalized
	// ContainerStatus, which deliberately doesn't expose labels) so this
	// test is a genuine check of what Docker itself recorded, not a
	// tautological check of our own translation code.
	result, err := rt.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("raw ContainerInspect: %v", err)
	}
	if result.Container.Config == nil {
		t.Fatal("inspected container has no Config")
	}
	labels := result.Container.Config.Labels
	if labels[runtime.LabelManaged] != "true" {
		t.Errorf("%s = %q, want \"true\"", runtime.LabelManaged, labels[runtime.LabelManaged])
	}
	if labels[runtime.LabelDeploymentID] != "abc-123" {
		t.Errorf("%s = %q, want %q", runtime.LabelDeploymentID, labels[runtime.LabelDeploymentID], "abc-123")
	}
	if labels[runtime.LabelDeploymentName] != "web" {
		t.Errorf("%s = %q, want %q", runtime.LabelDeploymentName, labels[runtime.LabelDeploymentName], "web")
	}
}

func TestDockerRuntimeCreateWithInvalidImageFails(t *testing.T) {
	rt := newTestRuntime(t)
	ctx := context.Background()

	_, err := rt.CreateContainer(ctx, runtime.ContainerSpec{
		Name:        uniqueName(t),
		Image:       "nimbus-this-image-definitely-does-not-exist:latest",
		CPU:         1,
		MemoryBytes: 32 * 1024 * 1024,
	})
	if err == nil {
		t.Fatal("CreateContainer with a nonexistent image succeeded, want an error")
	}
}

func TestDockerRuntimeOperationsOnNonexistentContainer(t *testing.T) {
	rt := newTestRuntime(t)
	ctx := context.Background()
	const missingID = "nimbus-test-nonexistent-container-id"

	if err := rt.StartContainer(ctx, missingID); !errors.Is(err, runtime.ErrContainerNotFound) {
		t.Errorf("StartContainer: error = %v, want ErrContainerNotFound", err)
	}
	if err := rt.StopContainer(ctx, missingID); !errors.Is(err, runtime.ErrContainerNotFound) {
		t.Errorf("StopContainer: error = %v, want ErrContainerNotFound", err)
	}
	if _, err := rt.InspectContainer(ctx, missingID); !errors.Is(err, runtime.ErrContainerNotFound) {
		t.Errorf("InspectContainer: error = %v, want ErrContainerNotFound", err)
	}
	// RemoveContainer is documented as idempotent: removing a container
	// Docker has never heard of is success, not an error.
	if err := rt.RemoveContainer(ctx, missingID); err != nil {
		t.Errorf("RemoveContainer: %v, want nil (idempotent)", err)
	}
}

func TestDockerRuntimeRepeatedStopAndRemove(t *testing.T) {
	rt := newTestRuntime(t)
	ctx := context.Background()

	id, err := rt.CreateContainer(ctx, runtime.ContainerSpec{
		Name: uniqueName(t), Image: testImageExitsAtOnce, CPU: 1, MemoryBytes: 32 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	cleanupContainer(t, rt, id)

	// A created-but-never-started container is already "stopped" in the
	// sense that matters here; stopping it twice must not error.
	if err := rt.StopContainer(ctx, id); err != nil {
		t.Fatalf("first StopContainer: %v", err)
	}
	if err := rt.StopContainer(ctx, id); err != nil {
		t.Errorf("second StopContainer: %v, want nil", err)
	}

	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Fatalf("first RemoveContainer: %v", err)
	}
	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Errorf("second RemoveContainer: %v, want nil (idempotent)", err)
	}
}

func TestDockerRuntimeRespectsContextCancellation(t *testing.T) {
	rt := newTestRuntime(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := rt.CreateContainer(ctx, runtime.ContainerSpec{
		Name: uniqueName(t), Image: testImageExitsAtOnce, CPU: 1, MemoryBytes: 32 * 1024 * 1024,
	})
	if err == nil {
		t.Fatal("CreateContainer with an already-cancelled context succeeded, want an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
}

func TestDockerRuntimeRespectsContextTimeout(t *testing.T) {
	rt := newTestRuntime(t)

	// An already-expired deadline, same intent as the cancellation test
	// above but exercising the timeout path specifically.
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	time.Sleep(time.Millisecond) // ensure the deadline has actually passed

	_, err := rt.CreateContainer(ctx, runtime.ContainerSpec{
		Name: uniqueName(t), Image: testImageExitsAtOnce, CPU: 1, MemoryBytes: 32 * 1024 * 1024,
	})
	if err == nil {
		t.Fatal("CreateContainer with an expired context deadline succeeded, want an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}
