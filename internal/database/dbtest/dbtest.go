// Package dbtest provisions a real, isolated PostgreSQL database for
// Nimbus's integration tests. It is not a _test.go file so that other
// packages' tests (internal/cluster, in particular) can import it; nothing
// in the production Control Plane or Node Agent binaries imports it, so it
// never ships in a built artifact.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhananjaiyadav1234/Nimbus/internal/database"
)

// Open returns a *sql.DB connected to a dedicated Nimbus test database, with
// every migration applied and every table truncated for a clean start. It
// skips the calling test (via t.Skip, never t.Fatal) when no PostgreSQL
// server is reachable, so `go test ./...` still passes for a developer who
// has not started Docker Compose — only `go test` runs that explicitly want
// the integration suite need it running.
//
// Connection settings come from the same NIMBUS_DATABASE_* environment
// variables the Control Plane itself reads (see internal/config), which
// `docker compose -f deployments/docker-compose.yml up -d` plus the
// repository's documented .env already satisfy. The test database itself is
// named "nimbus_test_<calling package>" (e.g. "nimbus_test_cluster",
// "nimbus_test_deployment") — one database per package, not one shared
// database for every package's integration tests — so these tests never
// touch a developer's real "nimbus" database, and, just as importantly, so
// two packages' test binaries running concurrently (which `go test ./...`
// does by default) never truncate or overwrite rows a different package's
// test just wrote to a database they happen to share. Set
// NIMBUS_TEST_DATABASE_NAME to force one specific shared name instead, if
// that's ever genuinely what's wanted.
func Open(t *testing.T) *sql.DB {
	t.Helper()

	settings := loadSettings(callerPackage())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := ensureTestDatabaseExists(ctx, settings); err != nil {
		t.Skipf("skipping integration test: PostgreSQL is not reachable (%v) — start it with `docker compose -f deployments/docker-compose.yml up -d`", err)
	}

	db, err := sql.Open("pgx", settings.dsn(settings.testDBName))
	if err != nil {
		t.Fatalf("dbtest: opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.PingContext(ctx); err != nil {
		t.Skipf("skipping integration test: PostgreSQL is not reachable (%v)", err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("dbtest: running migrations: %v", err)
	}

	truncateAll(t, db)
	return db
}

type settings struct {
	host, port, user, password, sslMode string
	testDBName                          string
	bootstrapDBName                     string
}

func loadSettings(pkg string) settings {
	return settings{
		host:            getenv("NIMBUS_DATABASE_HOST", "localhost"),
		port:            getenv("NIMBUS_DATABASE_PORT", "5432"),
		user:            getenv("NIMBUS_DATABASE_USER", "nimbus"),
		password:        getenv("NIMBUS_DATABASE_PASSWORD", "nimbus"),
		sslMode:         getenv("NIMBUS_DATABASE_SSL_MODE", "disable"),
		testDBName:      getenv("NIMBUS_TEST_DATABASE_NAME", "nimbus_test_"+pkg),
		bootstrapDBName: getenv("NIMBUS_DATABASE_NAME", "nimbus"),
	}
}

// callerPackage returns a Postgres-identifier-safe name derived from the
// package that called Open two frames up the stack (Open itself, then
// Open's own caller) — e.g. "deployment", "cluster", "database_test". Every
// package that calls Open gets its own test database name from this,
// without needing to pass one explicitly — see Open's doc comment for why
// that isolation matters.
func callerPackage() string {
	pc, _, _, ok := runtime.Caller(2) // 0=callerPackage, 1=Open, 2=Open's caller
	name := "unknown"
	if ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			name = fn.Name()
		}
	}

	// name looks like ".../internal/deployment.newTestRepository" or
	// ".../internal/database_test.TestMigrateIsSafeToRunRepeatedly" —
	// keep only the package's own last path segment.
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "."); i >= 0 {
		name = name[:i]
	}

	return sanitizeIdentifierPart(name)
}

// sanitizeIdentifierPart keeps callerPackage's output safe to splice
// directly into a CREATE DATABASE statement: Go package names are already
// well-behaved (no spaces, no quotes), but this is cheap insurance against
// ever doing otherwise.
func sanitizeIdentifierPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "pkg"
	}
	return b.String()
}

func (s settings) dsn(database string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(s.user, s.password),
		Host:     net.JoinHostPort(s.host, s.port),
		Path:     "/" + database,
		RawQuery: url.Values{"sslmode": {s.sslMode}, "connect_timeout": {"3"}}.Encode(),
	}
	return u.String()
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ensureTestDatabaseExists connects to the ordinary Nimbus development
// database (already created by Docker Compose) purely as a bootstrap
// connection, and issues CREATE DATABASE for the test database if it
// doesn't exist yet. CREATE DATABASE cannot run inside a transaction and has
// no IF NOT EXISTS form, so a concurrent "already exists" error from another
// test binary racing to create it is treated as success, not failure.
func ensureTestDatabaseExists(ctx context.Context, s settings) error {
	bootstrap, err := sql.Open("pgx", s.dsn(s.bootstrapDBName))
	if err != nil {
		return err
	}
	defer bootstrap.Close()

	if err := bootstrap.PingContext(ctx); err != nil {
		return err
	}

	_, err = bootstrap.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE %s`, quoteIdentifier(s.testDBName)))
	if err != nil && !isDuplicateDatabaseError(err) {
		return fmt.Errorf("creating test database: %w", err)
	}
	return nil
}

// isDuplicateDatabaseError reports whether err is PostgreSQL's "database
// already exists" error (SQLSTATE 42P04). pgx/v5's *pgconn.PgError carries a
// structured Code field, but importing pgconn solely to type-assert it here
// would pull another package into this small test helper for one check;
// matching on the driver's error text is sufficient and keeps dbtest's own
// dependency surface minimal.
func isDuplicateDatabaseError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "42P04")
}

// quoteIdentifier double-quotes a PostgreSQL identifier. It is used only
// with the per-package "nimbus_test_<package>" default (see callerPackage)
// or an operator-supplied NIMBUS_TEST_DATABASE_NAME in a local development
// environment, never with untrusted input.
func quoteIdentifier(name string) string {
	return `"` + name + `"`
}

// truncateAll clears every Nimbus table so each test file starts from an
// empty database, without paying the cost of recreating the schema.
func truncateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`TRUNCATE TABLE nodes, deployments`); err != nil {
		t.Fatalf("dbtest: truncating tables: %v", err)
	}
}
