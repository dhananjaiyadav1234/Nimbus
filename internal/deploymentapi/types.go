// Package deploymentapi defines the wire contract for Nimbus deployment
// manifests and the Control Plane's deployment HTTP API — shared by the
// Control Plane's handlers and the `nimbus` CLI, exactly as internal/clusterapi
// is shared by the Control Plane, Node Agent, and CLI for node membership.
//
// Manifest doubles as both the YAML file format (`nimbus deploy -f`) and the
// JSON API request body: the same struct carries both `yaml` and `json`
// struct tags, so there is exactly one definition of the manifest shape to
// keep in sync, not two.
//
// This package is intentionally pure data. It validates only the manifest
// *envelope* (apiVersion/kind — is this even a Nimbus Deployment manifest?)
// because that is a wire-format concern the CLI legitimately checks before
// ever making a network call. Business rules about the deployment itself —
// name format, image constraints, resource bounds, memory-string parsing —
// belong to internal/deployment's CreateInput.Validate and ParseMemory, and
// are deliberately not duplicated here: the Control Plane is the single
// source of truth for those rules, so the CLI never has a stale or
// diverging copy of them.
package deploymentapi

import "time"

// SupportedAPIVersion and DeploymentKind are the only manifest envelope
// values Phase 2.1 accepts. Centralized here so both the CLI's early
// envelope check and the Control Plane's handler cite the same constants.
const (
	SupportedAPIVersion = "nimbus/v1"
	DeploymentKind      = "Deployment"
)

// Manifest is a Nimbus Deployment manifest — the YAML file format for
// `nimbus deploy -f` and, marshaled to JSON, the POST /deployments request
// body.
type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

// Metadata identifies the deployment being described.
type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

// Spec is the desired state of the workload.
type Spec struct {
	Image    string `yaml:"image" json:"image"`
	Replicas int    `yaml:"replicas" json:"replicas"`
	// Resources is a pointer so a manifest that omits it entirely (as
	// opposed to supplying zero values) can be told apart — required per
	// Part 3 of the Phase 2.1 contract ("resources must be present").
	Resources *Resources `yaml:"resources" json:"resources"`
}

// Resources is the human-readable resource request. Memory is a string
// (e.g. "512Mi") deliberately, not a number: it is normalized to a
// canonical byte count exactly once, server-side, by
// internal/deployment.ParseMemory — see this package's own doc comment.
type Resources struct {
	CPU    int64  `yaml:"cpu" json:"cpu"`
	Memory string `yaml:"memory" json:"memory"`
}

// ValidateEnvelope checks only the manifest envelope — apiVersion and
// kind — not the deployment's own fields. The CLI calls this immediately
// after parsing a manifest file, so an obviously-wrong file (wrong
// apiVersion, not a Deployment, or missing a name entirely) fails locally
// before any network call; see Part 39 of the Phase 2.1 requirements ("CLI
// should fail before making an API request" on invalid input). It is not a
// substitute for the Control Plane's own validation, which always runs
// regardless of what a client checked.
func (m Manifest) ValidateEnvelope() error {
	var problems []string

	if m.APIVersion != SupportedAPIVersion {
		problems = append(problems, "apiVersion must be \""+SupportedAPIVersion+"\", got \""+m.APIVersion+"\"")
	}
	if m.Kind != DeploymentKind {
		problems = append(problems, "kind must be \""+DeploymentKind+"\", got \""+m.Kind+"\"")
	}
	if m.Metadata.Name == "" {
		problems = append(problems, "metadata.name must not be empty")
	}
	if m.Spec.Resources == nil {
		problems = append(problems, "spec.resources must be present")
	}

	if len(problems) == 0 {
		return nil
	}
	return &EnvelopeError{Problems: problems}
}

// EnvelopeError reports every envelope-level problem found at once.
type EnvelopeError struct {
	Problems []string
}

func (e *EnvelopeError) Error() string {
	msg := "invalid manifest:"
	for _, p := range e.Problems {
		msg += " " + p + ";"
	}
	return msg
}

// DeploymentDTO is a deployment as exposed over the API — the wire
// equivalent of deployment.Deployment, using JSON-friendly primitive types
// throughout (time.Time marshals as RFC3339 automatically, the same
// convention internal/clusterapi.NodeDTO uses).
type DeploymentDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	Replicas    int       `json:"replicas"`
	CPU         int64     `json:"cpu"`
	MemoryBytes int64     `json:"memory_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListDeploymentsResponse is the body of GET /deployments.
type ListDeploymentsResponse struct {
	Deployments []DeploymentDTO `json:"deployments"`
}
