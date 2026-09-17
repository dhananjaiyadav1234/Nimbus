package nodeagent

import (
	"os"
	"runtime"
	"testing"
)

func TestDiscoverMachineInfoUsesRealHostnameByDefault(t *testing.T) {
	info, err := discoverMachineInfo("")
	if err != nil {
		t.Fatalf("discoverMachineInfo: %v", err)
	}

	want, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname unavailable in this environment: %v", err)
	}
	if info.Hostname != want {
		t.Errorf("Hostname = %q, want %q", info.Hostname, want)
	}
}

func TestDiscoverMachineInfoHonoursNameOverride(t *testing.T) {
	info, err := discoverMachineInfo("simulated-node-a")
	if err != nil {
		t.Fatalf("discoverMachineInfo: %v", err)
	}
	if info.Hostname != "simulated-node-a" {
		t.Errorf("Hostname = %q, want the override %q", info.Hostname, "simulated-node-a")
	}
}

// This distinguishes "node identity" from "physical hostname": two agents on
// one machine, given different overrides, must report different hostnames
// to the Control Plane even though every other discovered field is the same
// real machine's.
func TestDiscoverMachineInfoTwoOverridesLookLikeDifferentNodes(t *testing.T) {
	a, err := discoverMachineInfo("node-a")
	if err != nil {
		t.Fatalf("discoverMachineInfo(node-a): %v", err)
	}
	b, err := discoverMachineInfo("node-b")
	if err != nil {
		t.Fatalf("discoverMachineInfo(node-b): %v", err)
	}

	if a.Hostname == b.Hostname {
		t.Error("two different overrides produced the same hostname")
	}
	if a.OS != b.OS || a.Architecture != b.Architecture || a.CPUCapacity != b.CPUCapacity {
		t.Error("machine facts differed between two calls on the same machine, want them identical")
	}
}

func TestDiscoverMachineInfoReportsPlausibleCapacity(t *testing.T) {
	info, err := discoverMachineInfo("")
	if err != nil {
		t.Fatalf("discoverMachineInfo: %v", err)
	}

	if info.CPUCapacity <= 0 {
		t.Errorf("CPUCapacity = %d, want > 0", info.CPUCapacity)
	}
	if info.MemoryCapacityBytes <= 0 {
		t.Errorf("MemoryCapacityBytes = %d, want > 0", info.MemoryCapacityBytes)
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Architecture != runtime.GOARCH {
		t.Errorf("Architecture = %q, want %q", info.Architecture, runtime.GOARCH)
	}
}

func TestTotalMemoryBytesOnThisPlatform(t *testing.T) {
	// Exercises the real, platform-specific implementation selected by the
	// build (machineinfo_darwin.go / machineinfo_linux.go / _other.go) —
	// not a mock of it.
	mem, err := totalMemoryBytes()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil {
			t.Fatalf("totalMemoryBytes: %v", err)
		}
		if mem <= 0 {
			t.Errorf("totalMemoryBytes = %d, want > 0", mem)
		}
	default:
		if err == nil {
			t.Error("totalMemoryBytes succeeded on an unsupported platform, want a clear error")
		}
	}
}
