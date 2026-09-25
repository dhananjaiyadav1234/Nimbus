// Package schedulerapi defines the JSON wire contract for Nimbus's
// scheduling HTTP API — shared by the Control Plane's handlers and the
// `nimbus` CLI, exactly as internal/clusterapi and internal/deploymentapi
// are shared for their own domains.
//
// Nothing in this package talks HTTP, SQL, or holds behaviour — it is pure
// data, safe for every side of the contract to import without pulling in
// the others.
package schedulerapi

// PlacementDTO is a single placement as exposed over the API — the wire
// equivalent of scheduler.Placement, using JSON-friendly primitive types.
// DeploymentID and the placement's own ID are deliberately omitted: both
// responses that carry PlacementDTO already name the deployment once, at
// the top level, and a placement's own row ID is an internal implementation
// detail no client needs.
type PlacementDTO struct {
	ReplicaIndex int    `json:"replicaIndex"`
	NodeID       string `json:"nodeId"`
	CPU          int64  `json:"cpu"`
	MemoryBytes  int64  `json:"memoryBytes"`
}

// ScheduleResponse is the body of a successful POST /deployments/{id}/schedule.
type ScheduleResponse struct {
	DeploymentID string         `json:"deploymentId"`
	Placements   []PlacementDTO `json:"placements"`
}

// PlacementsResponse is the body of GET /deployments/{id}/placements.
type PlacementsResponse struct {
	DeploymentID string         `json:"deploymentId"`
	Placements   []PlacementDTO `json:"placements"`
}
