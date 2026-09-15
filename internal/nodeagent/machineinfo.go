package nodeagent

import (
	"fmt"
	"os"
	"runtime"
)

// MachineInfo is what the agent discovers about the machine it is running
// on (or, for local multi-agent simulation, the machine it is pretending to
// be) and reports to the Control Plane at registration.
type MachineInfo struct {
	Hostname            string
	OS                  string
	Architecture        string
	CPUCapacity         int64
	MemoryCapacityBytes int64
}

// discoverMachineInfo gathers MachineInfo using the Go standard library
// where possible (runtime.GOOS, runtime.GOARCH, runtime.NumCPU,
// os.Hostname). Total memory is not something the standard library exposes
// portably, so it goes through totalMemoryBytes, a small per-platform
// implementation — see machineinfo_darwin.go and machineinfo_linux.go.
//
// nameOverride, if non-empty, replaces the discovered hostname. It exists so
// several agents on one development machine, which would otherwise all
// report the same real hostname, can be told apart in `nimbus node list`.
func discoverMachineInfo(nameOverride string) (MachineInfo, error) {
	hostname := nameOverride
	if hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			return MachineInfo{}, fmt.Errorf("nodeagent: discovering hostname: %w", err)
		}
		hostname = h
	}

	memBytes, err := totalMemoryBytes()
	if err != nil {
		return MachineInfo{}, fmt.Errorf("nodeagent: discovering memory capacity: %w", err)
	}
	if memBytes <= 0 {
		// Fail clearly rather than report a node with zero or negative
		// capacity, which the Control Plane's own validation would reject
		// anyway but with a far less specific error.
		return MachineInfo{}, fmt.Errorf("nodeagent: discovered memory capacity is not positive (%d bytes)", memBytes)
	}

	return MachineInfo{
		Hostname:            hostname,
		OS:                  runtime.GOOS,
		Architecture:        runtime.GOARCH,
		CPUCapacity:         int64(runtime.NumCPU()),
		MemoryCapacityBytes: memBytes,
	}, nil
}
