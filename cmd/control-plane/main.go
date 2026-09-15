// Command control-plane runs the Nimbus control plane service.
//
// Its job is dependency construction and wiring only: configuration, logging,
// the database handle and the HTTP server are built here and handed to each
// other. Request handling and lifecycle logic live in internal/controlplane.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
	"github.com/dhananjaiyadav1234/Nimbus/internal/controlplane"
	"github.com/dhananjaiyadav1234/Nimbus/internal/database"
	"github.com/dhananjaiyadav1234/Nimbus/internal/logging"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet (configuration can fail first), so the
		// fatal path always reports on stderr.
		fmt.Fprintf(os.Stderr, "nimbus control plane: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ctx is cancelled on SIGINT or SIGTERM and is the signal to shut down.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := logging.New(os.Stdout, logging.Options{
		Level:       cfg.LogLevel,
		Development: cfg.IsDevelopment(),
	})
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	logger.Info("nimbus control plane starting",
		slog.String("env", cfg.Env),
		slog.String("log_level", cfg.LogLevel),
	)

	// Connect verifies connectivity before returning, so reaching the next line
	// means PostgreSQL answered.
	db, err := database.Connect(ctx, cfg.Database)
	if err != nil {
		return err
	}
	logger.Info("connected to postgresql",
		slog.String("address", db.Host()),
		slog.String("database", db.Name()),
	)

	server, err := controlplane.New(controlplane.Options{
		Config:           cfg.Server,
		Logger:           logger,
		Readiness:        db,
		ReadinessTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		// Preserve both the original failure and any error closing the
		// database, mirroring how the shutdown path below combines errors.
		return errors.Join(err, closeDatabase(logger, db))
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()

	var runErr error
	select {
	case err := <-serverErr:
		// The listener failed on its own; shut the rest down and report why.
		runErr = err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Restore default signal handling so a second interrupt terminates
	// immediately instead of waiting out the shutdown budget.
	stop()

	// ctx is already cancelled at this point, so the shutdown gets a fresh
	// context; Shutdown applies the configured timeout itself.
	if err := server.Shutdown(context.Background()); err != nil {
		runErr = errors.Join(runErr, err)
	}

	if err := closeDatabase(logger, db); err != nil {
		runErr = errors.Join(runErr, err)
	}

	logger.Info("nimbus control plane stopped")
	return runErr
}

func closeDatabase(logger *slog.Logger, db *database.DB) error {
	logger.Info("closing database connection", slog.String("address", db.Host()))
	if err := db.Close(); err != nil {
		return err
	}
	logger.Info("database connection closed")
	return nil
}
