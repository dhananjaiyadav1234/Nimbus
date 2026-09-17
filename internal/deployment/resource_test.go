package deployment

import (
	"strings"
	"testing"
)

func TestParseMemoryValidQuantities(t *testing.T) {
	tests := map[string]int64{
		"1":     1,
		"512":   512,
		"1Ki":   1024,
		"512Mi": 512 * (1 << 20),
		"1Gi":   1 << 30,
		"1Ti":   1 << 40,
		"2Gi":   2 * (1 << 30),
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			got, err := ParseMemory(raw)
			if err != nil {
				t.Fatalf("ParseMemory(%q): %v", raw, err)
			}
			if got != want {
				t.Errorf("ParseMemory(%q) = %d, want %d", raw, got, want)
			}
		})
	}
}

func TestParseMemoryInvalidQuantities(t *testing.T) {
	tests := []string{
		"",
		"   ",
		"abc",
		"-512Mi",
		"0Mi",
		"0",
		"512mi",  // lowercase suffix not recognised (documented, case-sensitive)
		"512MB",  // decimal-style suffix not supported
		"Mi",     // no numeric part
		"512 Mi", // internal space
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseMemory(raw); err == nil {
				t.Errorf("ParseMemory(%q) succeeded, want an error", raw)
			}
		})
	}
}

func TestParseMemoryRejectsOverflow(t *testing.T) {
	// A numeric part large enough that multiplying by the Ti multiplier
	// would wrap around int64 if not checked before multiplying.
	_, err := ParseMemory("9223372036854775807Ti")
	if err == nil {
		t.Fatal("ParseMemory with an overflowing quantity succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("error = %q, want it to mention overflow", err)
	}
}

func TestParseMemoryRejectsAboveMaxBound(t *testing.T) {
	// Valid arithmetic (no overflow) but exceeds the documented sanity
	// ceiling.
	_, err := ParseMemory("2Ti") // MaxMemoryBytes is 1 << 40 == 1Ti
	if err == nil {
		t.Fatal("ParseMemory(2Ti) succeeded, want it to exceed MaxMemoryBytes")
	}
}

func TestParseMemoryAcceptsExactlyMaxBound(t *testing.T) {
	got, err := ParseMemory("1Ti")
	if err != nil {
		t.Fatalf("ParseMemory(1Ti): %v", err)
	}
	if got != MaxMemoryBytes {
		t.Errorf("ParseMemory(1Ti) = %d, want %d (MaxMemoryBytes)", got, MaxMemoryBytes)
	}
}
