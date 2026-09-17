package runtime

import "fmt"

// Nimbus-owned label keys attached to every container Nimbus creates, so
// ownership and provenance can be queried without parsing container names —
// see docs/workloads.md's "Container naming and labels" section. These are
// not yet read back by anything in Phase 2.1 (there is no reconciliation
// loop until Phase 3), but the labels are applied now so that loop has
// something reliable to query later instead of needing a data migration.
const (
	LabelManaged        = "nimbus.managed"
	LabelDeploymentID   = "nimbus.deployment"
	LabelDeploymentName = "nimbus.deployment-name"
)

// ContainerName deterministically names a Nimbus-managed container:
// "nimbus-<deployment-name>-<instance>". instance distinguishes one
// deployment's replicas from each other — Phase 2.1 has no scheduler to
// ever create more than a conceptual "replica 0", but the naming scheme is
// shaped for Phase 2.2 from the start rather than hard-coding a single
// instance and having to redesign it once replicas are real.
//
// deploymentName is assumed to already satisfy Nimbus's deployment name
// rule (internal/deployment's nameRE — lowercase alphanumeric and interior
// hyphens), which is itself chosen to be safe inside a Docker container
// name without further escaping.
func ContainerName(deploymentName string, instance int) string {
	return fmt.Sprintf("nimbus-%s-%d", deploymentName, instance)
}

// ContainerLabels returns the standard Nimbus labels for a container
// belonging to deploymentID/deploymentName.
func ContainerLabels(deploymentID, deploymentName string) map[string]string {
	return map[string]string{
		LabelManaged:        "true",
		LabelDeploymentID:   deploymentID,
		LabelDeploymentName: deploymentName,
	}
}
