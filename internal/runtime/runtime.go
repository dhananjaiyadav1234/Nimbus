// Package runtime defines Nimbus's container runtime boundary: the
// ContainerRuntime interface every Docker-specific detail stays behind.
//
// Nothing above this package — the Node Agent's business logic, and every
// future caller such as Phase 2.2's scheduler — should import a Docker
// package directly or know Docker exists. It depends on ContainerRuntime,
// constructed once at wiring time (main.go) with a concrete implementation
// such as internal/runtime/docker.DockerRuntime, exactly the same shape as
// internal/cluster.Repository or internal/controlplane.NodeService.
package runtime

import (
	"context"
	"errors"
)

// ErrContainerNotFound is returned by StartContainer, StopContainer, and
// InspectContainer when the given container ID does not exist. It is not
// returned by RemoveContainer: removing an already-absent container is
// defined as success (see ContainerRuntime.RemoveContainer).
var ErrContainerNotFound = errors.New("runtime: container not found")

// ContainerSpec is Nimbus's own container specification. Runtime
// implementations translate it into whatever their backend needs —
// DockerRuntime turns it into Docker's container.Config/HostConfig — so
// nothing above this package ever constructs a backend-specific type.
type ContainerSpec struct {
	// Name is the deterministic, Nimbus-owned container name — see
	// internal/runtime's naming convention in docs/workloads.md. Must be
	// unique per running container.
	Name string
	// Image is the image reference to run, e.g. "nginx:latest". The
	// runtime does not pull it implicitly — see DockerRuntime's doc comment.
	Image string
	// CPU is the number of CPUs to grant the container, in the same whole-
	// core unit as deployment.Deployment.CPU.
	CPU int64
	// MemoryBytes is the memory limit in bytes.
	MemoryBytes int64
	// Labels are attached to the created container so it (and everything
	// Nimbus created it for) can be identified and queried later without
	// relying on name parsing — see docs/workloads.md's "Container
	// naming and labels" section.
	Labels map[string]string
}

// ContainerStatus is Nimbus's normalized view of a container's state.
// Runtime implementations translate their backend's own inspect result into
// this shape — DockerRuntime never leaks a Docker SDK type past its own
// package boundary.
type ContainerStatus struct {
	ID    string
	Name  string
	Image string
	// State is the runtime's own state name (e.g. Docker's "running",
	// "exited", "created") — informational, and intentionally not an enum:
	// Running is the one normalized, backend-independent fact Nimbus logic
	// should actually branch on.
	State   string
	Running bool
}

// ContainerRuntime is the boundary between Nimbus and whatever actually
// runs containers. Every method is context-aware so a caller can bound or
// cancel a call that talks to a real container engine over the network/a
// socket.
type ContainerRuntime interface {
	// CreateContainer creates (but does not start) a container from spec
	// and returns the backend's container ID.
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)

	// StartContainer starts an existing, created container.
	StartContainer(ctx context.Context, id string) error

	// StopContainer stops a running container, giving it a bounded grace
	// period to exit before the runtime forces it — see DockerRuntime's
	// stopTimeout. Stopping an already-stopped container is not an error.
	StopContainer(ctx context.Context, id string) error

	// RemoveContainer removes a container. It is idempotent: removing a
	// container that no longer exists returns nil, not
	// ErrContainerNotFound — a caller cleaning up should not have to treat
	// "already gone" as a failure.
	RemoveContainer(ctx context.Context, id string) error

	// InspectContainer returns id's current normalized status.
	InspectContainer(ctx context.Context, id string) (ContainerStatus, error)
}
