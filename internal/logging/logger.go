// Package logging builds the structured logger used across Nimbus.
//
// Nimbus logs with the standard library's log/slog so that every component
// emits machine-parseable, key/value structured records without pulling in a
// logging framework.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Options controls how the logger is constructed.
type Options struct {
	// Level is the minimum severity to emit: debug, info, warn or error.
	Level string
	// Development selects the human-readable text handler. Any other
	// environment gets JSON, which is what log shippers expect.
	Development bool
}

// New returns a structured logger writing to w.
//
// It returns an error only for an unrecognised level; configuration is
// validated before this point, so a failure here indicates programmer error
// rather than bad operator input.
func New(w io.Writer, opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}

	handlerOpts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if opts.Development {
		handler = slog.NewTextHandler(w, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(w, handlerOpts)
	}

	return slog.New(handler), nil
}

// ParseLevel converts a configured level name into an slog.Level.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: unknown level %q", name)
	}
}
