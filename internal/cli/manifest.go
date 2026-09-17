package cli

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
)

// ParseManifestFile reads and parses a Nimbus Deployment manifest YAML file
// (`nimbus deploy -f`), then validates its envelope — see
// deploymentapi.Manifest.ValidateEnvelope for exactly what that checks
// (apiVersion/kind/name/resources presence) and, just as importantly, what
// it deliberately leaves to the Control Plane. This is Nimbus's only
// YAML-handling code, kept at this one boundary per docs/workloads.md.
func ParseManifestFile(path string) (deploymentapi.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return deploymentapi.Manifest{}, fmt.Errorf("reading manifest file %s: %w", path, err)
	}

	var m deploymentapi.Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return deploymentapi.Manifest{}, fmt.Errorf("parsing manifest file %s: %w", path, err)
	}

	if err := m.ValidateEnvelope(); err != nil {
		return deploymentapi.Manifest{}, err
	}

	return m, nil
}
