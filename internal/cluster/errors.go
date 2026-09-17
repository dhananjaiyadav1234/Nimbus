package cluster

import "errors"

// ErrNotFound is returned by Service methods when a node ID does not exist.
// Handlers translate it to HTTP 404; it is never itself exposed to clients.
var ErrNotFound = errors.New("cluster: node not found")
