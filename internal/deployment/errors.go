package deployment

import "errors"

// ErrNotFound is returned by Service/Repository methods when a deployment
// ID or name does not exist. Handlers translate it to HTTP 404; it is never
// itself exposed to clients.
var ErrNotFound = errors.New("deployment: not found")

// ErrAlreadyExists is returned when a deployment name is already taken.
// PostgreSQL's UNIQUE constraint on the name column is what actually
// enforces this — see repository.go — so this error is correct even under
// concurrent creation, not merely an application-level pre-check. Handlers
// translate it to HTTP 409.
var ErrAlreadyExists = errors.New("deployment: name already exists")
