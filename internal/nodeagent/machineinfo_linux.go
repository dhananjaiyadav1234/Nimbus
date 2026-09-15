//go:build linux

package nodeagent

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// totalMemoryBytes reads the machine's physical memory from /proc/meminfo's
// "MemTotal" line, which the kernel reports in kibibytes.
func totalMemoryBytes() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("opening /proc/meminfo: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected MemTotal line format: %q", line)
		}
		kib, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing MemTotal value %q: %w", fields[1], err)
		}
		return kib * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("reading /proc/meminfo: %w", err)
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}
