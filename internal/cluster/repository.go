package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Repository persists nodes in PostgreSQL. It is the only place in Nimbus
// that issues SQL against the `nodes` table; Service depends on this
// interface rather than a concrete type so it can be exercised in tests
// without a database.
type Repository interface {
	// Upsert creates node if its ID is new, or updates the existing row if
	// not — preserving that row's original RegisteredAt. It is safe to call
	// concurrently for the same ID: the write is a single atomic statement,
	// not a read-then-write.
	Upsert(ctx context.Context, node Node) (Node, error)

	// Get returns the node with the given ID, or ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (Node, error)

	// List returns every known node ordered by hostname ascending — see
	// docs/cluster-membership.md for why hostname was chosen.
	List(ctx context.Context) ([]Node, error)

	// RecordHeartbeat sets last_heartbeat_at and updated_at to receivedAt —
	// the Control Plane's own clock, never a value supplied by the agent —
	// and unconditionally moves the node to StatusReady, returning both the
	// updated node and the status it had immediately before this call (so a
	// NotReady -> Ready recovery can be told apart from an ordinary
	// Ready -> Ready heartbeat and logged accordingly). Returns ErrNotFound
	// if id does not exist: heartbeats never create a node.
	RecordHeartbeat(ctx context.Context, id uuid.UUID, receivedAt time.Time) (node Node, previousStatus Status, err error)

	// MarkStale flips every StatusReady node whose last_heartbeat_at is
	// older than cutoff to StatusNotReady, stamping updated_at with now, and
	// returns exactly the nodes that transitioned. The update is a single
	// statement scoped by a WHERE clause, so it cannot race a concurrent
	// heartbeat: whichever write commits first determines the outcome.
	MarkStale(ctx context.Context, cutoff, now time.Time) ([]Node, error)
}

// postgresRepository is the PostgreSQL-backed Repository.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository builds a Repository backed by db. The `nodes` table
// must already exist — see internal/database's migration runner.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

const nodeColumns = `id, hostname, status, os, architecture, cpu_capacity, memory_capacity_bytes, agent_version, last_heartbeat_at, registered_at, updated_at`

func (r *postgresRepository) Upsert(ctx context.Context, node Node) (Node, error) {
	const query = `
		INSERT INTO nodes (id, hostname, status, os, architecture, cpu_capacity, memory_capacity_bytes, agent_version, last_heartbeat_at, registered_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			hostname               = EXCLUDED.hostname,
			status                 = EXCLUDED.status,
			os                     = EXCLUDED.os,
			architecture           = EXCLUDED.architecture,
			cpu_capacity           = EXCLUDED.cpu_capacity,
			memory_capacity_bytes  = EXCLUDED.memory_capacity_bytes,
			agent_version          = EXCLUDED.agent_version,
			last_heartbeat_at      = EXCLUDED.last_heartbeat_at,
			updated_at             = EXCLUDED.updated_at
			-- registered_at is deliberately absent: identity's original
			-- registration time is preserved across re-registration.
		RETURNING ` + nodeColumns

	row := r.db.QueryRowContext(ctx, query,
		node.ID, node.Hostname, string(node.Status), node.OS, node.Architecture,
		node.CPUCapacity, node.MemoryCapacityBytes, node.AgentVersion,
		node.LastHeartbeatAt, node.RegisteredAt, node.UpdatedAt,
	)

	saved, err := scanNode(row)
	if err != nil {
		return Node{}, fmt.Errorf("cluster: upserting node %s: %w", node.ID, err)
	}
	return saved, nil
}

func (r *postgresRepository) Get(ctx context.Context, id uuid.UUID) (Node, error) {
	const query = `SELECT ` + nodeColumns + ` FROM nodes WHERE id = $1`

	node, err := scanNode(r.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("cluster: getting node %s: %w", id, err)
	}
	return node, nil
}

func (r *postgresRepository) List(ctx context.Context) ([]Node, error) {
	const query = `SELECT ` + nodeColumns + ` FROM nodes ORDER BY hostname ASC, id ASC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("cluster: listing nodes: %w", err)
	}
	defer rows.Close()

	nodes := []Node{}
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("cluster: listing nodes: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cluster: listing nodes: %w", err)
	}
	return nodes, nil
}

func (r *postgresRepository) RecordHeartbeat(ctx context.Context, id uuid.UUID, receivedAt time.Time) (Node, Status, error) {
	// The FROM subquery reads the row's pre-update status from the same
	// MVCC snapshot the UPDATE itself starts from, so `prev.status` is
	// reliably the value from immediately before this statement — not a
	// value that could have changed underneath a separate SELECT-then-UPDATE.
	const query = `
		UPDATE nodes AS n
		SET last_heartbeat_at = $2, updated_at = $2, status = 'Ready'
		FROM (SELECT status FROM nodes WHERE id = $1) AS prev
		WHERE n.id = $1
		RETURNING n.id, n.hostname, n.status, n.os, n.architecture, n.cpu_capacity, n.memory_capacity_bytes, n.agent_version, n.last_heartbeat_at, n.registered_at, n.updated_at, prev.status`

	var previousStatus string
	row := r.db.QueryRowContext(ctx, query, id, receivedAt)
	node, err := scanNodeWithExtra(row, &previousStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, "", ErrNotFound
	}
	if err != nil {
		return Node{}, "", fmt.Errorf("cluster: recording heartbeat for node %s: %w", id, err)
	}
	return node, Status(previousStatus), nil
}

func (r *postgresRepository) MarkStale(ctx context.Context, cutoff, now time.Time) ([]Node, error) {
	// last_heartbeat_at IS NULL is treated as "definitely overdue" alongside
	// the ordinary < cutoff case. No current code path ever persists a Ready
	// node with a NULL last_heartbeat_at (Service.Register always sets it),
	// but the column is nullable at the schema level, and without this
	// clause a row that somehow ended up in that state would be silently
	// exempt from failure detection forever: `NULL < cutoff` evaluates to
	// NULL/false in SQL, not true, so the plain comparison alone would never
	// match it.
	const query = `
		UPDATE nodes
		SET status = 'NotReady', updated_at = $2
		WHERE status = 'Ready' AND (last_heartbeat_at IS NULL OR last_heartbeat_at < $1)
		RETURNING ` + nodeColumns

	rows, err := r.db.QueryContext(ctx, query, cutoff, now)
	if err != nil {
		return nil, fmt.Errorf("cluster: marking stale nodes: %w", err)
	}
	defer rows.Close()

	stale := []Node{}
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("cluster: marking stale nodes: %w", err)
		}
		stale = append(stale, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cluster: marking stale nodes: %w", err)
	}
	return stale, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scanNode works
// for single-row and multi-row queries alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner) (Node, error) {
	var (
		node   Node
		status string
	)
	err := row.Scan(
		&node.ID, &node.Hostname, &status, &node.OS, &node.Architecture,
		&node.CPUCapacity, &node.MemoryCapacityBytes, &node.AgentVersion,
		&node.LastHeartbeatAt, &node.RegisteredAt, &node.UpdatedAt,
	)
	if err != nil {
		return Node{}, err
	}
	node.Status = Status(status)
	return node, nil
}

// scanNodeWithExtra scans a node row that carries one additional trailing
// column (dest) beyond the standard nodeColumns set.
func scanNodeWithExtra(row rowScanner, dest *string) (Node, error) {
	var (
		node   Node
		status string
	)
	err := row.Scan(
		&node.ID, &node.Hostname, &status, &node.OS, &node.Architecture,
		&node.CPUCapacity, &node.MemoryCapacityBytes, &node.AgentVersion,
		&node.LastHeartbeatAt, &node.RegisteredAt, &node.UpdatedAt,
		dest,
	)
	if err != nil {
		return Node{}, err
	}
	node.Status = Status(status)
	return node, nil
}
