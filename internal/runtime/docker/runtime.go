// Package docker implements internal/runtime.ContainerRuntime against a
// real Docker Engine, via Docker's own Go client talking to the Engine API
// — never the Docker CLI, and never a shelled-out `docker` command. See
// docs/workloads.md for the accompanying integration test instructions.
package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/dhananjaiyadav1234/Nimbus/internal/runtime"
)

// stopTimeout bounds how long StopContainer waits for the container's own
// process to exit (via its stop signal, SIGTERM by default) before Docker
// forces it with SIGKILL.
const stopTimeout = 10 * time.Second

// Runtime implements runtime.ContainerRuntime against a real Docker Engine.
type Runtime struct {
	cli *client.Client
}

// New builds a Runtime. It negotiates the Docker Engine API version rather
// than pinning one, so it keeps working across Docker Engine upgrades.
// Connection settings come from the standard Docker environment variables
// (DOCKER_HOST, DOCKER_CERT_PATH, DOCKER_TLS_VERIFY) via client.FromEnv,
// matching what the `docker` CLI itself honours.
func New() (*Runtime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker: building client: %w", err)
	}
	return &Runtime{cli: cli}, nil
}

// Close releases the underlying HTTP client's connections.
func (r *Runtime) Close() error {
	return r.cli.Close()
}

// Ping verifies the Docker Engine is reachable. It is not part of
// runtime.ContainerRuntime — nothing in Phase 2.1 calls it in production —
// but it is useful both for this package's own integration tests (skip
// cleanly if there is no Docker daemon, exactly as
// internal/database/dbtest does for PostgreSQL) and as a natural building
// block for a future Node Agent readiness check.
func (r *Runtime) Ping(ctx context.Context) error {
	_, err := r.cli.Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf("docker: ping: %w", err)
	}
	return nil
}

// CreateContainer creates a container from spec and returns its Docker
// container ID. It does not start the container, and it does not pull
// spec.Image implicitly — the image must already be present on this Docker
// Engine (see docs/workloads.md); Docker reports a clear "No such image"
// error otherwise, which this method returns wrapped, not swallowed.
func (r *Runtime) CreateContainer(ctx context.Context, spec runtime.ContainerSpec) (string, error) {
	// Image is set on Config only, not the sibling options.Image shortcut —
	// the client rejects a call that sets both ("either Image or
	// config.Image should be set").
	result, err := r.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image:  spec.Image,
			Labels: spec.Labels,
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				NanoCPUs: spec.CPU * 1_000_000_000,
				Memory:   spec.MemoryBytes,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("docker: creating container %q: %w", spec.Name, err)
	}
	return result.ID, nil
}

// StartContainer starts an existing, created container.
func (r *Runtime) StartContainer(ctx context.Context, id string) error {
	_, err := r.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	if cerrdefs.IsNotFound(err) {
		return runtime.ErrContainerNotFound
	}
	if err != nil {
		return fmt.Errorf("docker: starting container %s: %w", id, err)
	}
	return nil
}

// StopContainer stops a running container, giving it up to stopTimeout to
// exit on its own before Docker forces it. Stopping an already-stopped
// container is not an error — Docker's own stop endpoint is idempotent in
// exactly this way, so no special-casing is needed here.
func (r *Runtime) StopContainer(ctx context.Context, id string) error {
	timeoutSeconds := int(stopTimeout.Seconds())
	_, err := r.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeoutSeconds})
	if cerrdefs.IsNotFound(err) {
		return runtime.ErrContainerNotFound
	}
	if err != nil {
		return fmt.Errorf("docker: stopping container %s: %w", id, err)
	}
	return nil
}

// RemoveContainer removes a container. Per runtime.ContainerRuntime, this
// is idempotent: removing a container that Docker already doesn't know
// about is treated as success, not ErrContainerNotFound.
func (r *Runtime) RemoveContainer(ctx context.Context, id string) error {
	_, err := r.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
	if err == nil || cerrdefs.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("docker: removing container %s: %w", id, err)
}

// InspectContainer returns id's current normalized status.
func (r *Runtime) InspectContainer(ctx context.Context, id string) (runtime.ContainerStatus, error) {
	result, err := r.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return runtime.ContainerStatus{}, runtime.ErrContainerNotFound
	}
	if err != nil {
		return runtime.ContainerStatus{}, fmt.Errorf("docker: inspecting container %s: %w", id, err)
	}

	info := result.Container
	status := runtime.ContainerStatus{
		ID:   info.ID,
		Name: strings.TrimPrefix(info.Name, "/"), // Docker's own inspect always prefixes the name with "/"
	}
	if info.Config != nil {
		// Config.Image preserves the image reference as requested
		// (e.g. "nginx:latest"); InspectResponse.Image is the resolved
		// image ID/digest, which is not what Nimbus callers expect here.
		status.Image = info.Config.Image
	}
	if info.State != nil {
		status.State = string(info.State.Status)
		status.Running = info.State.Running
	}
	return status, nil
}
