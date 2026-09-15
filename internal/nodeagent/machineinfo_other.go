//go:build !darwin && !linux

package nodeagent

import (
	"fmt"
	"runtime"
)

// totalMemoryBytes has no implementation on platforms other than macOS and
// Linux. It fails clearly, per docs/cluster-membership.md's requirement
// that an agent report a real error rather than a fabricated memory figure
// when it cannot determine one reliably.
func totalMemoryBytes() (int64, error) {
	return 0, fmt.Errorf("nodeagent: memory discovery is not implemented on %s", runtime.GOOS)
}
