// Package nodeagent implements the Nimbus Node Agent: the process that runs
// on (or, for local development, simulates) a worker machine, registers it
// with the Control Plane, and keeps it marked alive with heartbeats.
package nodeagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// identityFileName is the file an agent's identity is persisted to, inside
// its configured NodeDataDir.
const identityFileName = "identity.json"

// identity is what the agent persists locally so a restart reuses the same
// node ID instead of registering as a brand-new node every time.
type identity struct {
	NodeID uuid.UUID `json:"node_id"`
}

// loadOrCreateIdentity reads the identity file under dataDir, creating both
// the directory and a fresh identity (a freshly generated UUID) if neither
// exists yet. A second agent process pointed at the same dataDir later reuses
// the identity this call persists — that reuse is what keeps a restarted
// agent's node ID stable.
func loadOrCreateIdentity(dataDir string) (identity, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return identity{}, fmt.Errorf("nodeagent: creating node data directory %s: %w", dataDir, err)
	}

	path := filepath.Join(dataDir, identityFileName)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var id identity
		if jsonErr := json.Unmarshal(raw, &id); jsonErr != nil {
			return identity{}, fmt.Errorf("nodeagent: identity file %s is corrupt: %w", path, jsonErr)
		}
		if id.NodeID == uuid.Nil {
			return identity{}, fmt.Errorf("nodeagent: identity file %s has an empty node_id", path)
		}
		return id, nil

	case os.IsNotExist(err):
		id := identity{NodeID: uuid.New()}
		if writeErr := writeIdentity(path, id); writeErr != nil {
			return identity{}, writeErr
		}
		return id, nil

	default:
		return identity{}, fmt.Errorf("nodeagent: reading identity file %s: %w", path, err)
	}
}

// writeIdentity persists id atomically: it writes to a temporary file in the
// same directory and renames it into place, so a crash mid-write can never
// leave a half-written, corrupt identity file behind.
func writeIdentity(path string, id identity) error {
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("nodeagent: encoding identity: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("nodeagent: writing identity file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("nodeagent: committing identity file: %w", err)
	}
	return nil
}
