// Package controlplane implements the Nimbus control plane HTTP service.
//
// Phase 1.1 serves only the liveness and readiness probes. The server owns the
// routing table and its own lifecycle (start and graceful shutdown); main.go is
// responsible only for constructing dependencies and wiring them together.
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
	"github.com/dhananjaiyadav1234/Nimbus/internal/health"
)

// Conservative HTTP timeouts. They protect the process from slow or abandoned
// clients and are not operator-tunable in Phase 1.1 because the only endpoints
// are small, fast probes.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
)

// Options are the dependencies required to build a Server.
type Options struct {
	// Config supplies the listen address and shutdown budget.
	Config config.ServerConfig
	// Logger receives the server's structured records. Required.
	Logger *slog.Logger
	// Readiness is the dependency probed by GET /ready. Required.
	Readiness health.Checker
	// ReadinessTimeout bounds a single readiness check.
	ReadinessTimeout time.Duration
	// Nodes backs every /nodes* route. Required.
	Nodes NodeService
}

// Server is the control plane HTTP service.
type Server struct {
	http            *http.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

// New builds a control plane server. It returns an error when a required
// dependency is missing, which keeps the failure at wiring time rather than on
// the first request.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		return nil, errors.New("controlplane: logger is required")
	}
	if opts.Readiness == nil {
		return nil, errors.New("controlplane: readiness checker is required")
	}
	if opts.ReadinessTimeout <= 0 {
		return nil, errors.New("controlplane: readiness timeout must be greater than zero")
	}
	if opts.Nodes == nil {
		return nil, errors.New("controlplane: node service is required")
	}

	handler := newRouter(&api{
		logger:           opts.Logger,
		readiness:        opts.Readiness,
		readinessTimeout: opts.ReadinessTimeout,
		nodes:            opts.Nodes,
	})

	return &Server{
		http: &http.Server{
			Addr:              opts.Config.Address(),
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		},
		logger:          opts.Logger,
		shutdownTimeout: opts.Config.ShutdownTimeout,
	}, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.http.Addr }

// ListenAndServe blocks serving HTTP until the server is shut down.
//
// A graceful shutdown is a normal outcome, so http.ErrServerClosed is reported
// as success; anything else is a real failure.
func (s *Server) ListenAndServe() error {
	s.logger.Info("http server started", slog.String("address", s.http.Addr))

	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("controlplane: http server failed: %w", err)
	}
	return nil
}

// Shutdown stops accepting new connections and waits for in-flight requests to
// finish, up to the configured shutdown timeout.
//
// The caller's context is honoured as an upper bound, so a second interrupt can
// cut the wait short.
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	s.logger.Info("http server shutting down", slog.Duration("timeout", s.shutdownTimeout))

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("controlplane: graceful shutdown failed: %w", err)
	}

	s.logger.Info("http server stopped")
	return nil
}
