// Package database owns the control plane's PostgreSQL connectivity.
//
// Phase 1.1 establishes the connection and nothing more: there are no Nimbus
// tables, no migrations and no repositories yet. The package is nevertheless
// shaped so those can be added without disturbing callers — everything reaches
// PostgreSQL through the *DB handle returned by Connect, so a future
// `migrations` step or a `repository` type can take that same handle.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	// Registers the "pgx" driver with database/sql. pgx is the actively
	// maintained PostgreSQL driver for Go; using it through database/sql keeps
	// the rest of Nimbus on standard library interfaces.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
)

// DB is a handle to the Nimbus PostgreSQL database.
type DB struct {
	sql *sql.DB

	// host and name are retained purely for log fields. Credentials are
	// deliberately not stored on the handle so they cannot leak into logs.
	host string
	name string
}

// Connect opens a connection pool and verifies that PostgreSQL answers.
//
// The returned handle is ready for use; the caller owns it and must Close it.
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*DB, error) {
	pool, err := sql.Open("pgx", dsn(cfg))
	if err != nil {
		// sql.Open only validates the DSN, so an error here means the DSN is
		// malformed. It may embed the password, so it is not wrapped.
		return nil, fmt.Errorf("database: invalid connection settings for %s", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	}

	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	db := &DB{sql: pool, host: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), name: cfg.Name}

	checkCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := db.Check(checkCtx); err != nil {
		// Do not leak a half-open pool if the very first check fails.
		_ = pool.Close()
		return nil, err
	}

	return db, nil
}

// Check verifies that the database answers within the caller's context.
//
// It satisfies health.Checker, which is how GET /ready reaches PostgreSQL.
func (db *DB) Check(ctx context.Context) error {
	if err := db.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("database: ping %s failed: %w", db.host, err)
	}
	return nil
}

// Close releases every pooled connection. It is safe to call more than once.
func (db *DB) Close() error {
	if err := db.sql.Close(); err != nil {
		return fmt.Errorf("database: closing connection pool: %w", err)
	}
	return nil
}

// SQL exposes the underlying pool so later phases can add migrations and
// repositories without this package having to grow a method per query.
func (db *DB) SQL() *sql.DB { return db.sql }

// Host returns the host:port the handle is connected to. Safe to log.
func (db *DB) Host() string { return db.host }

// Name returns the database name. Safe to log.
func (db *DB) Name() string { return db.name }

// dsn builds a PostgreSQL connection URL.
//
// url.UserPassword escapes the credentials, so passwords containing reserved
// characters (@, /, :) do not corrupt the DSN. The result contains the password
// in clear text and must never be logged.
func dsn(cfg config.DatabaseConfig) string {
	query := url.Values{}
	query.Set("sslmode", cfg.SSLMode)
	query.Set("connect_timeout", strconv.Itoa(connectTimeoutSeconds(cfg.ConnectTimeout)))

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Name,
		RawQuery: query.Encode(),
	}
	return u.String()
}

// connectTimeoutSeconds converts a duration to libpq's whole-second
// connect_timeout parameter, rounding sub-second values up to 1 so a short
// timeout never becomes "wait forever".
func connectTimeoutSeconds(d time.Duration) int {
	seconds := int(d / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}
