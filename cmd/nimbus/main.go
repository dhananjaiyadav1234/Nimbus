// Command nimbus is Nimbus's command-line client: a thin wrapper over the
// Control Plane's HTTP API for cluster inspection. It never talks to
// PostgreSQL directly (see docs/cluster-membership.md) and, per Phase 1.2's
// scope, currently supports exactly one command: `nimbus node list`.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/cli"
)

// defaultControlPlaneURL matches cmd/control-plane's own default listen
// port, so `nimbus node list` works out of the box against a Control Plane
// started with no configuration overrides.
const defaultControlPlaneURL = "http://localhost:8080"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 || args[0] != "node" || args[1] != "list" {
		return fmt.Errorf("usage: nimbus node list")
	}

	fs := flag.NewFlagSet("nimbus node list", flag.ContinueOnError)
	controlPlaneURL := fs.String("control-plane", envOrDefault("NIMBUS_CONTROL_PLANE_URL", defaultControlPlaneURL),
		"Nimbus Control Plane base URL (overrides NIMBUS_CONTROL_PLANE_URL)")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	client := cli.NewControlPlaneClient(*controlPlaneURL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListNodes(ctx)
	if err != nil {
		// FriendlyError strips connection-refused/dial detail and internal
		// server errors down to a short, user-safe sentence — see its own
		// doc comment for exactly what it does and does not expose.
		return fmt.Errorf("%s", cli.FriendlyError(err))
	}

	return cli.RenderNodesTable(os.Stdout, resp.Nodes, time.Now())
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
