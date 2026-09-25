package scheduler

import "errors"

// ErrDeploymentNotFound is returned by Service.Schedule and
// Service.Placements when the given deployment ID does not exist. Handlers
// translate it to HTTP 404; it is never itself exposed to clients.
var ErrDeploymentNotFound = errors.New("scheduler: deployment not found")

// ErrInsufficientCapacity is returned when the Ready nodes in the cluster,
// after accounting for every already-persisted placement, cannot fit every
// replica this scheduling attempt still needs to place. It always wraps a
// more specific message (which replica, what it needed) — see decide in
// scheduler.go — but callers that only need to distinguish "capacity
// problem" from "some other failure" can match it with errors.Is.
//
// When this is returned, zero new placements have been persisted — see
// Repository.Schedule's doc comment for the atomicity guarantee this
// depends on.
var ErrInsufficientCapacity = errors.New("scheduler: insufficient cluster capacity")

// ErrResourceOverflow is returned when a resource-arithmetic operation
// (see resources.go) would not fit in an int64. In practice this is
// unreachable through the HTTP API — deployment.CreateInput.Validate and
// cluster.RegisterInput.Validate already bound CPU/memory values to sane
// ranges — but the arithmetic itself never assumes that and always
// reports this instead of silently wrapping.
var ErrResourceOverflow = errors.New("scheduler: resource arithmetic overflow")
