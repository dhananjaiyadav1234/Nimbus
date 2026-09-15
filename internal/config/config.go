// Package config loads and validates the Nimbus control plane configuration
// from environment variables.
//
// Configuration is read exactly once, at startup, and passed explicitly to the
// components that need it. No other package in Nimbus reads the environment
// directly, which keeps the set of supported settings discoverable in one place
// and keeps every component trivially constructible in tests.
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Environment names recognised by Nimbus.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// Config is the fully resolved configuration for a control plane process.
type Config struct {
	// Env is the deployment environment (development, staging, production).
	Env string
	// LogLevel is the minimum severity that is emitted (debug, info, warn, error).
	LogLevel string

	Server   ServerConfig
	Database DatabaseConfig
}

// ServerConfig configures the control plane HTTP listener.
type ServerConfig struct {
	Host string
	Port int

	// ShutdownTimeout bounds how long a graceful shutdown waits for in-flight
	// requests to complete before the server is stopped forcefully.
	ShutdownTimeout time.Duration
}

// DatabaseConfig configures the PostgreSQL connection.
type DatabaseConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration

	// ConnectTimeout bounds a single connectivity check: the validation
	// performed at startup and each GET /ready probe.
	ConnectTimeout time.Duration
}

// Address returns the host:port the HTTP server should listen on.
func (s ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// IsDevelopment reports whether the process is running in development mode.
func (c Config) IsDevelopment() bool { return c.Env == EnvDevelopment }

var (
	validEnvs      = []string{EnvDevelopment, EnvStaging, EnvProduction}
	validLogLevels = []string{"debug", "info", "warn", "error"}
	// Permitted libpq sslmode values.
	validSSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
)

// Load reads the configuration from the process environment, applies defaults
// suitable for local development, and validates the result.
//
// All problems are reported together rather than one per run, so a
// misconfigured deployment can be fixed in a single pass.
func Load() (*Config, error) {
	var l loader

	cfg := &Config{
		Env:      l.str("NIMBUS_ENV", EnvDevelopment),
		LogLevel: l.str("NIMBUS_LOG_LEVEL", "info"),
		Server: ServerConfig{
			Host:            l.str("NIMBUS_CONTROL_PLANE_HOST", "0.0.0.0"),
			Port:            l.intVal("NIMBUS_CONTROL_PLANE_PORT", 8080),
			ShutdownTimeout: l.duration("NIMBUS_CONTROL_PLANE_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Database: DatabaseConfig{
			Host: l.str("NIMBUS_DATABASE_HOST", "localhost"),
			Port: l.intVal("NIMBUS_DATABASE_PORT", 5432),
			Name: l.str("NIMBUS_DATABASE_NAME", "nimbus"),
			User: l.str("NIMBUS_DATABASE_USER", "nimbus"),
			// Deliberately has no default: a credential must always come from
			// the environment, never from the source tree.
			Password:        l.str("NIMBUS_DATABASE_PASSWORD", ""),
			SSLMode:         l.str("NIMBUS_DATABASE_SSL_MODE", "disable"),
			MaxOpenConns:    l.intVal("NIMBUS_DATABASE_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    l.intVal("NIMBUS_DATABASE_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: l.duration("NIMBUS_DATABASE_CONN_MAX_LIFETIME", 30*time.Minute),
			ConnectTimeout:  l.duration("NIMBUS_DATABASE_CONNECT_TIMEOUT", 5*time.Second),
		},
	}

	errs := append(l.errs, cfg.validate()...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func (c Config) validate() []error {
	var errs []error

	if !slices.Contains(validEnvs, c.Env) {
		errs = append(errs, fmt.Errorf("NIMBUS_ENV: %q is not one of %s", c.Env, strings.Join(validEnvs, ", ")))
	}
	if !slices.Contains(validLogLevels, strings.ToLower(c.LogLevel)) {
		errs = append(errs, fmt.Errorf("NIMBUS_LOG_LEVEL: %q is not one of %s", c.LogLevel, strings.Join(validLogLevels, ", ")))
	}

	if c.Server.Host == "" {
		errs = append(errs, errors.New("NIMBUS_CONTROL_PLANE_HOST: must not be empty"))
	}
	errs = appendIfInvalidPort(errs, "NIMBUS_CONTROL_PLANE_PORT", c.Server.Port)
	errs = appendIfNotPositive(errs, "NIMBUS_CONTROL_PLANE_SHUTDOWN_TIMEOUT", c.Server.ShutdownTimeout)

	if c.Database.Host == "" {
		errs = append(errs, errors.New("NIMBUS_DATABASE_HOST: must not be empty"))
	}
	errs = appendIfInvalidPort(errs, "NIMBUS_DATABASE_PORT", c.Database.Port)
	if c.Database.Name == "" {
		errs = append(errs, errors.New("NIMBUS_DATABASE_NAME: must not be empty"))
	}
	if c.Database.User == "" {
		errs = append(errs, errors.New("NIMBUS_DATABASE_USER: must not be empty"))
	}
	if c.Database.Password == "" {
		errs = append(errs, errors.New("NIMBUS_DATABASE_PASSWORD: is required and has no default"))
	}
	if !slices.Contains(validSSLModes, c.Database.SSLMode) {
		errs = append(errs, fmt.Errorf("NIMBUS_DATABASE_SSL_MODE: %q is not one of %s", c.Database.SSLMode, strings.Join(validSSLModes, ", ")))
	}
	if c.Database.MaxOpenConns < 1 {
		errs = append(errs, fmt.Errorf("NIMBUS_DATABASE_MAX_OPEN_CONNS: must be at least 1, got %d", c.Database.MaxOpenConns))
	}
	if c.Database.MaxIdleConns < 0 {
		errs = append(errs, fmt.Errorf("NIMBUS_DATABASE_MAX_IDLE_CONNS: must not be negative, got %d", c.Database.MaxIdleConns))
	}
	if c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		errs = append(errs, fmt.Errorf("NIMBUS_DATABASE_MAX_IDLE_CONNS (%d): must not exceed NIMBUS_DATABASE_MAX_OPEN_CONNS (%d)",
			c.Database.MaxIdleConns, c.Database.MaxOpenConns))
	}
	errs = appendIfNotPositive(errs, "NIMBUS_DATABASE_CONN_MAX_LIFETIME", c.Database.ConnMaxLifetime)
	errs = appendIfNotPositive(errs, "NIMBUS_DATABASE_CONNECT_TIMEOUT", c.Database.ConnectTimeout)

	return errs
}

// loader reads typed values from the environment, accumulating parse failures
// so that every bad variable is reported in one error rather than one per run.
type loader struct {
	errs []error
}

func (l *loader) str(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func (l *loader) intVal(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %q is not a valid integer", key, raw))
		return fallback
	}
	return v
}

func (l *loader) duration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %q is not a valid duration (e.g. 15s, 1m, 30m)", key, raw))
		return fallback
	}
	return v
}

func appendIfInvalidPort(errs []error, key string, port int) []error {
	if port < 1 || port > 65535 {
		return append(errs, fmt.Errorf("%s: %d is outside the valid port range 1-65535", key, port))
	}
	return errs
}

func appendIfNotPositive(errs []error, key string, d time.Duration) []error {
	if d <= 0 {
		return append(errs, fmt.Errorf("%s: must be greater than zero, got %s", key, d))
	}
	return errs
}
