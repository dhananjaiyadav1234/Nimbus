package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/deploymentapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/schedulerapi"
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
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		// Node heartbeats rarely reach this branch (a node that far behind
		// is long since NotReady), but a deployment's AGE routinely does —
		// "3d ago" reads far better than "743h ago".
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
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

// RenderPlacementsTable writes placements to w as an aligned,
// human-readable table:
//
//	REPLICA     NODE ID                                CPU     MEMORY
//	0           3fa85f64-5717-4562-b3fc-2c963f66afa6   1       512 MiB
//
// An empty placements slice still prints the header — the caller (see
// cmd/nimbus) is responsible for telling the operator that means "not
// scheduled yet" rather than leaving a bare header to speak for itself.
func RenderPlacementsTable(w io.Writer, placements []schedulerapi.PlacementDTO) error {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)

	if _, err := fmt.Fprintln(tw, "REPLICA\tNODE ID\tCPU\tMEMORY"); err != nil {
		return err
	}
	for _, p := range placements {
		_, err := fmt.Fprintf(tw, "%d\t%s\t%d\t%s\n", p.ReplicaIndex, p.NodeID, p.CPU, FormatMemory(p.MemoryBytes))
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

// RenderDeploymentsTable writes deployments to w as an aligned,
// human-readable table:
//
//	NAME     IMAGE          REPLICAS     CPU     MEMORY     AGE
//	web      nginx:latest   2            1       512 MiB    2m ago
func RenderDeploymentsTable(w io.Writer, deployments []deploymentapi.DeploymentDTO, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)

	if _, err := fmt.Fprintln(tw, "NAME\tIMAGE\tREPLICAS\tCPU\tMEMORY\tAGE"); err != nil {
		return err
	}
	for _, d := range deployments {
		createdAt := d.CreatedAt
		_, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n",
			d.Name, d.Image, d.Replicas, d.CPU, FormatMemory(d.MemoryBytes), FormatRelativeAge(&createdAt, now),
		)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}
