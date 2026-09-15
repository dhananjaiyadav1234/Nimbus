package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

func TestFormatMemory(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KiB"},
		{1048576, "1 MiB"},
		{8589934592, "8 GiB"},
		{17179869184, "16 GiB"},
	}
	for _, tc := range tests {
		if got := FormatMemory(tc.bytes); got != tc.want {
			t.Errorf("FormatMemory(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestFormatRelativeAge(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    *time.Time
		want string
	}{
		{"never heartbeated", nil, "never"},
		{"2 seconds ago", ptr(now.Add(-2 * time.Second)), "2s ago"},
		{"41 seconds ago", ptr(now.Add(-41 * time.Second)), "41s ago"},
		{"90 seconds ago", ptr(now.Add(-90 * time.Second)), "1m ago"},
		{"2 hours ago", ptr(now.Add(-2 * time.Hour)), "2h ago"},
		{"in the future (clock skew)", ptr(now.Add(5 * time.Second)), "0s ago"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRelativeAge(tc.t, now); got != tc.want {
				t.Errorf("FormatRelativeAge(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestRenderNodesTableFormatsExpectedColumns(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nodes := []clusterapi.NodeDTO{
		{Hostname: "node-a", Status: "Ready", CPUCapacity: 8, MemoryCapacityBytes: 17179869184, LastHeartbeatAt: ptr(now.Add(-2 * time.Second))},
		{Hostname: "node-b", Status: "NotReady", CPUCapacity: 4, MemoryCapacityBytes: 8589934592, LastHeartbeatAt: ptr(now.Add(-41 * time.Second))},
	}

	var buf bytes.Buffer
	if err := RenderNodesTable(&buf, nodes, now); err != nil {
		t.Fatalf("RenderNodesTable: %v", err)
	}

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 nodes):\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "STATUS") ||
		!strings.Contains(lines[0], "CPU") || !strings.Contains(lines[0], "MEMORY") ||
		!strings.Contains(lines[0], "LAST HEARTBEAT") {
		t.Errorf("header line missing expected columns: %q", lines[0])
	}
	if !strings.Contains(lines[1], "node-a") || !strings.Contains(lines[1], "Ready") ||
		!strings.Contains(lines[1], "16 GiB") || !strings.Contains(lines[1], "2s ago") {
		t.Errorf("node-a row missing expected content: %q", lines[1])
	}
	if !strings.Contains(lines[2], "node-b") || !strings.Contains(lines[2], "NotReady") ||
		!strings.Contains(lines[2], "8 GiB") || !strings.Contains(lines[2], "41s ago") {
		t.Errorf("node-b row missing expected content: %q", lines[2])
	}
}

func TestRenderNodesTableEmptyClusterStillPrintsHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderNodesTable(&buf, nil, time.Now()); err != nil {
		t.Fatalf("RenderNodesTable: %v", err)
	}
	if !strings.Contains(buf.String(), "NAME") {
		t.Errorf("empty cluster output missing header: %q", buf.String())
	}
}
