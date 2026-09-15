package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
)

// FormatMemory renders a byte count as a human-readable binary size, e.g.
// 17179869184 -> "16 GiB". It always rounds to a whole unit — this is a
// terminal listing, not a precision report.
func FormatMemory(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FormatRelativeAge renders how long ago t was, relative to now, the way
// `nimbus node list` shows LAST HEARTBEAT (e.g. "2s ago", "41s ago"). A node
// that has never heartbeated (nil t) reports "never".
func FormatRelativeAge(t *time.Time, now time.Time) string {
	if t == nil {
		return "never"
	}

	d := now.Sub(*t)
	if d < 0 {
		d = 0
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// RenderNodesTable writes nodes to w as an aligned, human-readable table:
//
//	NAME       STATUS     CPU     MEMORY     LAST HEARTBEAT
//	node-a     Ready      8       16 GiB     2s ago
func RenderNodesTable(w io.Writer, nodes []clusterapi.NodeDTO, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)

	if _, err := fmt.Fprintln(tw, "NAME\tSTATUS\tCPU\tMEMORY\tLAST HEARTBEAT"); err != nil {
		return err
	}
	for _, n := range nodes {
		_, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			n.Hostname, n.Status, n.CPUCapacity, FormatMemory(n.MemoryCapacityBytes), FormatRelativeAge(n.LastHeartbeatAt, now),
		)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}
