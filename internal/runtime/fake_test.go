package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestFakeRuntimeLifecycle(t *testing.T) {
	rt := NewFakeRuntime()
	ctx := context.Background()
	spec := ContainerSpec{Name: "nimbus-web-0", Image: "nginx:latest", CPU: 1, MemoryBytes: 1 << 20}

	id, err := rt.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if id == "" {
		t.Fatal("CreateContainer returned an empty ID")
	}

	status, err := rt.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer (after create): %v", err)
	}
	if status.Running {
		t.Error("Running = true immediately after create, want false")
	}
	if status.Name != spec.Name || status.Image != spec.Image {
		t.Errorf("status = %+v, want Name=%q Image=%q", status, spec.Name, spec.Image)
	}

	if err := rt.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	status, err = rt.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer (after start): %v", err)
	}
	if !status.Running {
		t.Error("Running = false after StartContainer, want true")
	}

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

	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
	if _, err := rt.InspectContainer(ctx, id); !errors.Is(err, ErrContainerNotFound) {
		t.Errorf("InspectContainer after remove: error = %v, want ErrContainerNotFound", err)
	}
}

func TestFakeRuntimeRemoveIsIdempotent(t *testing.T) {
	rt := NewFakeRuntime()
	ctx := context.Background()

	id, err := rt.CreateContainer(ctx, ContainerSpec{Name: "x", Image: "y"})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Fatalf("first RemoveContainer: %v", err)
	}
	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Errorf("second RemoveContainer: %v, want nil (idempotent)", err)
	}
	if err := rt.RemoveContainer(ctx, "never-existed"); err != nil {
		t.Errorf("RemoveContainer on an unknown ID: %v, want nil (idempotent)", err)
	}
}

func TestFakeRuntimeOperationsOnUnknownContainer(t *testing.T) {
	rt := NewFakeRuntime()
	ctx := context.Background()

	if err := rt.StartContainer(ctx, "missing"); !errors.Is(err, ErrContainerNotFound) {
		t.Errorf("StartContainer: error = %v, want ErrContainerNotFound", err)
	}
	if err := rt.StopContainer(ctx, "missing"); !errors.Is(err, ErrContainerNotFound) {
		t.Errorf("StopContainer: error = %v, want ErrContainerNotFound", err)
	}
	if _, err := rt.InspectContainer(ctx, "missing"); !errors.Is(err, ErrContainerNotFound) {
		t.Errorf("InspectContainer: error = %v, want ErrContainerNotFound", err)
	}
}

func TestFakeRuntimeCreateErrIsReturned(t *testing.T) {
	rt := NewFakeRuntime()
	rt.CreateErr = errors.New("simulated runtime unavailable")

	_, err := rt.CreateContainer(context.Background(), ContainerSpec{Name: "x", Image: "y"})
	if !errors.Is(err, rt.CreateErr) {
		t.Errorf("CreateContainer error = %v, want %v", err, rt.CreateErr)
	}
}

func TestContainerNameIsDeterministicAndDistinguishesInstances(t *testing.T) {
	a := ContainerName("web", 0)
	b := ContainerName("web", 1)
	c := ContainerName("web", 0)

	if a == b {
		t.Errorf("ContainerName(web, 0) == ContainerName(web, 1) = %q, want different names", a)
	}
	if a != c {
		t.Errorf("ContainerName is not deterministic: %q != %q", a, c)
	}
	if a != "nimbus-web-0" {
		t.Errorf("ContainerName(web, 0) = %q, want %q", a, "nimbus-web-0")
	}
}

func TestContainerLabels(t *testing.T) {
	labels := ContainerLabels("abc-123", "web")

	if labels[LabelManaged] != "true" {
		t.Errorf("%s = %q, want \"true\"", LabelManaged, labels[LabelManaged])
	}
	if labels[LabelDeploymentID] != "abc-123" {
		t.Errorf("%s = %q, want %q", LabelDeploymentID, labels[LabelDeploymentID], "abc-123")
	}
	if labels[LabelDeploymentName] != "web" {
		t.Errorf("%s = %q, want %q", LabelDeploymentName, labels[LabelDeploymentName], "web")
	}
}
