// Package clusterapi defines the JSON wire contract between the Control
// Plane's node HTTP API and its clients — the Node Agent and the `nimbus`
// CLI. All three live in this one module, so they share these types rather
// than each independently duplicating the same JSON shape: a field renamed
// here fails to compile everywhere it matters, instead of silently drifting.
//
// Nothing in this package talks HTTP, SQL, or holds behaviour — it is pure
// data, safe for every side of the contract to import without pulling in
// the others.
package clusterapi

import "time"

// RegisterRequest is the body of POST /nodes/register.
type RegisterRequest struct {
	// NodeID is empty for a node's first-ever registration. An agent that
	// already has a persisted identity sends it here so the Control Plane
	// updates that same node instead of creating a new one.
	NodeID              string `json:"node_id,omitempty"`
	Hostname            string `json:"hostname"`
	OS                  string `json:"os"`
	Architecture        string `json:"architecture"`
	CPUCapacity         int64  `json:"cpu_capacity"`
	MemoryCapacityBytes int64  `json:"memory_capacity_bytes"`
	AgentVersion        string `json:"agent_version"`
}

// RegisterResponse is the body of a successful POST /nodes/register.
type RegisterResponse struct {
	NodeID                   string `json:"node_id"`
	Status                   string `json:"status"`
	HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds"`
}

// HeartbeatRequest is the body of POST /nodes/{id}/heartbeat. Timestamp is
// optional and, if present, is logged/informational only — the Control
// Plane's own receipt time is always what decides liveness, never a value
// supplied by the agent, so clock skew between machines cannot affect
// membership decisions.
type HeartbeatRequest struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// HeartbeatResponse is the body of a successful heartbeat.
type HeartbeatResponse struct {
	Status string `json:"status"`
}

// NodeDTO is a node as exposed over the API — the wire equivalent of
// cluster.Node, using JSON-friendly primitive types throughout.
type NodeDTO struct {
	ID                  string     `json:"id"`
	Hostname            string     `json:"hostname"`
	Status              string     `json:"status"`
	OS                  string     `json:"os"`
	Architecture        string     `json:"architecture"`
	CPUCapacity         int64      `json:"cpu_capacity"`
	MemoryCapacityBytes int64      `json:"memory_capacity_bytes"`
	AgentVersion        string     `json:"agent_version"`
	LastHeartbeatAt     *time.Time `json:"last_heartbeat_at"`
	RegisteredAt        time.Time  `json:"registered_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ListNodesResponse is the body of GET /nodes.
type ListNodesResponse struct {
	Nodes []NodeDTO `json:"nodes"`
}
