package deployment

import (
	"fmt"
	"regexp"
)

// nameRE enforces a deterministic, deliberately small naming rule: lowercase
// alphanumerics and interior hyphens, starting and ending with an
// alphanumeric. This is the same shape Kubernetes and most container
// tooling use for resource names, adopted here not to imitate Kubernetes
// but because it is a simple, well-understood rule that keeps a Deployment
// name safe to use directly in container names and labels (see
// internal/runtime's naming convention) without further escaping.
var nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Validate applies every field-level rule from the Deployment contract,
// returning a *ValidationError naming every problem at once so a caller
// with several mistakes fixes them in one round trip rather than one per
// retry — the same pattern internal/cluster.RegisterInput.Validate uses.
func (in CreateInput) Validate() error {
	var err ValidationError

	if in.Name == "" {
		err.add("name must not be empty")
	} else if len(in.Name) > MaxNameLength {
		err.add(fmt.Sprintf("name must not exceed %d characters", MaxNameLength))
	} else if !nameRE.MatchString(in.Name) {
		err.add("name must match " + nameRE.String() + " (lowercase alphanumeric, hyphens allowed between characters)")
	}

	if in.Image == "" {
		err.add("image must not be empty")
	} else if len(in.Image) > MaxImageLength {
		err.add(fmt.Sprintf("image must not exceed %d characters", MaxImageLength))
	} else if hasWhitespaceOrControl(in.Image) {
		err.add("image must not contain whitespace or control characters")
	}

	if in.Replicas < 0 {
		err.add("replicas must not be negative")
	} else if in.Replicas > MaxReplicas {
		err.add(fmt.Sprintf("replicas must not exceed %d", MaxReplicas))
	}

	if in.CPU <= 0 {
		err.add("cpu must be greater than zero")
	} else if in.CPU > MaxCPU {
		err.add(fmt.Sprintf("cpu must not exceed %d", MaxCPU))
	}

	if in.MemoryBytes <= 0 {
		err.add("memory must be greater than zero")
	} else if in.MemoryBytes > MaxMemoryBytes {
		err.add(fmt.Sprintf("memory must not exceed %d bytes", MaxMemoryBytes))
	}

	if len(err.Problems) > 0 {
		return &err
	}
	return nil
}

func hasWhitespaceOrControl(s string) bool {
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

// ValidationError reports every problem found with a client-supplied
// CreateInput in one value, so handlers can return a single, complete 400
// response — the same shape as internal/cluster.ValidationError.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) add(problem string) {
	e.Problems = append(e.Problems, problem)
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d validation problems: %v", len(e.Problems), e.Problems)
}
