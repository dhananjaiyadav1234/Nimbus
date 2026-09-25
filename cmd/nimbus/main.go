// Command nimbus is Nimbus's command-line client: a thin wrapper over the
// Control Plane's HTTP API. It never talks to PostgreSQL or Docker
// directly — every command below goes through the Control Plane's HTTP API
// only (see docs/cluster-membership.md and docs/workloads.md).
//
// Commands:
//
//	nimbus node list
//	nimbus deployment list
//	nimbus deploy -f <manifest.yaml>
//	nimbus deployment schedule <name|id>
//	nimbus deployment placements <name|id>
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
// port, so every command below works out of the box against a Control
// Plane started with no configuration overrides.
const defaultControlPlaneURL = "http://localhost:8080"

// requestTimeout bounds how long a single command waits on the Control
// Plane before giving up.
const requestTimeout = 10 * time.Second

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "node":
		if len(args) >= 2 && args[1] == "list" {
			return runNodeList(args[2:])
		}
	case "deployment":
		if len(args) >= 2 {
			switch args[1] {
			case "list":
				return runDeploymentList(args[2:])
			case "schedule":
				return runDeploymentSchedule(args[2:])
			case "placements":
				return runDeploymentPlacements(args[2:])
			}
		}
	case "deploy":
		return runDeploy(args[1:])
	}
	return errUsage
}

var errUsage = fmt.Errorf("usage:\n  nimbus node list\n  nimbus deployment list\n  nimbus deploy -f <manifest.yaml>\n  nimbus deployment schedule <name|id>\n  nimbus deployment placements <name|id>")

func runNodeList(args []string) error {
	fs := flag.NewFlagSet("nimbus node list", flag.ContinueOnError)
	controlPlaneURL := controlPlaneFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := cli.NewControlPlaneClient(*controlPlaneURL)
	resp, err := client.ListNodes(ctx)
	if err != nil {
		return friendlyErr(err)
	}
	return cli.RenderNodesTable(os.Stdout, resp.Nodes, time.Now())
}

func runDeploymentList(args []string) error {
	fs := flag.NewFlagSet("nimbus deployment list", flag.ContinueOnError)
	controlPlaneURL := controlPlaneFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := cli.NewControlPlaneClient(*controlPlaneURL)
	resp, err := client.ListDeployments(ctx)
	if err != nil {
		return friendlyErr(err)
	}
	return cli.RenderDeploymentsTable(os.Stdout, resp.Deployments, time.Now())
}

// runDeploy implements the CLI side of `nimbus deploy -f <file>`:
// read the manifest file, parse it, validate its envelope (all inside
// cli.ParseManifestFile — see its doc comment), send it to the Control
// Plane, and display the result. It never touches PostgreSQL or Docker.
func runDeploy(args []string) error {
	fs := flag.NewFlagSet("nimbus deploy", flag.ContinueOnError)
	controlPlaneURL := controlPlaneFlag(fs)
	file := fs.String("f", "", "path to a Nimbus Deployment manifest YAML file (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("usage: nimbus deploy -f <manifest.yaml>")
	}

	// Parsed and envelope-validated entirely client-side, before any
	// network call — an obviously-wrong file (bad apiVersion, missing
	// name, ...) fails here rather than after a round trip.
	manifest, err := cli.ParseManifestFile(*file)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := cli.NewControlPlaneClient(*controlPlaneURL)
	deployment, err := client.CreateDeployment(ctx, manifest)
	if err != nil {
		return friendlyErr(err)
	}

	fmt.Printf("deployment %q created (id: %s, replicas: %d)\n", deployment.Name, deployment.ID, deployment.Replicas)
	return nil
}

// runDeploymentSchedule implements `nimbus deployment schedule <name|id>`.
// This is the only CLI command that ever calls the scheduling endpoint —
// `nimbus deploy` never triggers scheduling itself, matching the Control
// Plane's own persistence-only POST /deployments contract. It contains no
// scheduling logic of its own: resolving name to ID and rendering the
// result are the only local work, everything else is the Control Plane's
// POST /deployments/{id}/schedule response.
func runDeploymentSchedule(args []string) error {
	fs := flag.NewFlagSet("nimbus deployment schedule", flag.ContinueOnError)
	controlPlaneURL := controlPlaneFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: nimbus deployment schedule <name|id>")
	}
	nameOrID := fs.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := cli.NewControlPlaneClient(*controlPlaneURL)
	id, err := cli.ResolveDeploymentID(ctx, client, nameOrID)
	if err != nil {
		return friendlyErr(err)
	}

	resp, err := client.ScheduleDeployment(ctx, id)
	if err != nil {
		return friendlyErr(err)
	}

	fmt.Printf("deployment %q scheduled (%d placement(s))\n", nameOrID, len(resp.Placements))
	return cli.RenderPlacementsTable(os.Stdout, resp.Placements)
}

// runDeploymentPlacements implements `nimbus deployment placements <name|id>`.
func runDeploymentPlacements(args []string) error {
	fs := flag.NewFlagSet("nimbus deployment placements", flag.ContinueOnError)
	controlPlaneURL := controlPlaneFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: nimbus deployment placements <name|id>")
	}
	nameOrID := fs.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := cli.NewControlPlaneClient(*controlPlaneURL)
	id, err := cli.ResolveDeploymentID(ctx, client, nameOrID)
	if err != nil {
		return friendlyErr(err)
	}

	resp, err := client.GetPlacements(ctx, id)
	if err != nil {
		return friendlyErr(err)
	}
	if len(resp.Placements) == 0 {
		fmt.Printf("deployment %q has no placements yet — run `nimbus deployment schedule %s`\n", nameOrID, nameOrID)
		return nil
	}
	return cli.RenderPlacementsTable(os.Stdout, resp.Placements)
}

func controlPlaneFlag(fs *flag.FlagSet) *string {
	return fs.String("control-plane", envOrDefault("NIMBUS_CONTROL_PLANE_URL", defaultControlPlaneURL),
		"Nimbus Control Plane base URL (overrides NIMBUS_CONTROL_PLANE_URL)")
}

// friendlyErr wraps cli.FriendlyError as an error value. FriendlyError
// strips connection-refused/dial detail and internal server errors down to
// a short, user-safe sentence — see its own doc comment for exactly what it
// does and does not expose.
func friendlyErr(err error) error {
	return fmt.Errorf("%s", cli.FriendlyError(err))
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
