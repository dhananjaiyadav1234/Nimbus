package deployment

import (
	"fmt"
	"strconv"
	"strings"
)

// memoryUnits maps the IEC binary suffixes Nimbus accepts to their byte
// multiplier. Only binary (1024-based) suffixes are supported — the
// decimal suffixes Kubernetes also accepts (K, M, G, T) are deliberately
// left out: nothing in the Phase 2.1 contract asks for them, "Ki"/"Mi"/
// "Gi"/"Ti" already cover the one documented example (memory: 512Mi)
// unambiguously, and supporting both systems invites exactly the kind of
// unit confusion this normalization step exists to prevent.
var memoryUnits = map[string]int64{
	"":   1,
	"Ki": 1 << 10,
	"Mi": 1 << 20,
	"Gi": 1 << 30,
	"Ti": 1 << 40,
}

// ParseMemory normalizes a human-readable memory quantity (e.g. "512Mi",
// "1Gi", or a bare byte count like "536870912") into a canonical byte count.
// This is the only place in Nimbus a memory string is turned into a number
// — everything past this function, including the database column, the
// domain model, and the container runtime, deals exclusively in int64
// bytes, never ambiguous strings.
func ParseMemory(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("memory quantity must not be empty")
	}

	numEnd := len(raw)
	for numEnd > 0 && !isDigit(raw[numEnd-1]) {
		numEnd--
	}
	numberPart, suffix := raw[:numEnd], raw[numEnd:]

	multiplier, ok := memoryUnits[suffix]
	if !ok {
		return 0, fmt.Errorf("memory quantity %q has an unrecognised unit %q (supported: Ki, Mi, Gi, Ti, or a bare byte count)", raw, suffix)
	}

	value, err := strconv.ParseInt(numberPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("memory quantity %q has an invalid numeric part %q: %w", raw, numberPart, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("memory quantity %q must be a positive number", raw)
	}

	// Overflow check before multiplying, not after: value*multiplier can
	// silently wrap around int64 for large enough inputs (e.g. a
	// maliciously large numeric part with a "Ti" suffix), which would
	// otherwise turn an absurd request into a small, accepted one.
	if multiplier > 1 && value > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("memory quantity %q overflows: too large to represent in bytes", raw)
	}

	bytes := value * multiplier
	if bytes > MaxMemoryBytes {
		return 0, fmt.Errorf("memory quantity %q (%d bytes) exceeds the maximum of %d bytes", raw, bytes, MaxMemoryBytes)
	}

	return bytes, nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
