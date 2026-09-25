package controlplane

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
	"github.com/dhananjaiyadav1234/Nimbus/internal/health"
)

func testOptions() Options {
	return Options{
		Config: config.ServerConfig{
			Host:            "127.0.0.1",
			Port:            8080,
			ShutdownTimeout: time.Second,
		},
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness:        health.CheckerFunc(func(context.Context) error { return nil }),
		ReadinessTimeout: time.Second,
		Nodes:            &stubNodeService{},
		Deployments:      &stubDeploymentService{},
		Scheduler:        &stubSchedulerService{},
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	tests := map[string]func(*Options){
		"missing logger":             func(o *Options) { o.Logger = nil },
		"missing readiness checker":  func(o *Options) { o.Readiness = nil },
		"zero readiness timeout":     func(o *Options) { o.ReadinessTimeout = 0 },
		"missing node service":       func(o *Options) { o.Nodes = nil },
		"missing deployment service": func(o *Options) { o.Deployments = nil },
		"missing scheduler service":  func(o *Options) { o.Scheduler = nil },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			opts := testOptions()
			mutate(&opts)

			if _, err := New(opts); err == nil {
				t.Fatal("New succeeded, want an error")
			}
		})
	}
}

func TestNewListensOnTheConfiguredAddress(t *testing.T) {
	server, err := New(testOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got, want := server.Addr(), "127.0.0.1:8080"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

// Shutting down a server that never started must still return cleanly, which is
// the path taken when the listener fails at startup.
func TestShutdownOnUnstartedServer(t *testing.T) {
	server, err := New(testOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
