// Command node-agent runs a single Nimbus node agent: it registers this
// machine (or, for local development, a simulated node) with the Control
// Plane and heartbeats until told to stop.
//
// Its job, like cmd/control-plane, is dependency construction and wiring
// only; the actual identity, registration and heartbeat behaviour lives in
// internal/nodeagent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
	"github.com/dhananjaiyadav1234/Nimbus/internal/logging"
	"github.com/dhananjaiyadav1234/Nimbus/internal/nodeagent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nimbus node agent: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ctx is cancelled on SIGINT or SIGTERM and is the signal to shut down.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadNodeAgent()
	if err != nil {
		return err
	}

	logger, err := logging.New(os.Stdout, logging.Options{
		// The node agent has no NIMBUS_ENV of its own; text output is only
		// really useful for interactive local development, which is also
		// the only setting multiple agents get run side by side in, so it
		// defaults on. JSON remains available by piping through a formatter
		// if an operator wants it.
		Development: true,
		Level:       "info",
	})
	if err != nil {
		return err
	}

	agent := nodeagent.New(*cfg, logger, nil)

	if err := agent.Run(ctx); err != nil {
		return err
	}
	return nil
}
