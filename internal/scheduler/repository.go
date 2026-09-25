package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// Repository persists placements in PostgreSQL. It is the only place in
// Nimbus that issues SQL against the `deployment_placements` table, and the
// only place that reads `nodes`/`deployments` for scheduling purposes —
// Service depends on this interface rather than a concrete type so it can
// be exercised in tests without a database, mirroring
// internal/cluster.Repository and internal/deployment.Repository.
//
// Reading two other domains' tables (`nodes`, `deployments`) from this
// package is a deliberate exception to "one table per repository": the
// scheduler's correctness requirement — that cluster-wide capacity
// accounting and placement writes happen atomically under row locks the
// scheduler itself controls — cannot be met by composing calls to
// cluster.Repository and deployment.Repository, which know nothing of each
// other's transactions. See Schedule's doc comment for exactly what it
// reads, locks, and writes, and docs/scheduling.md's "Concurrency" section
// for the full design rationale.
type Repository interface {
	// ListByDeployment returns every persisted placement for deploymentID,
	// ordered by replica index ascending. It does not error if
	// deploymentID does not exist or has no placements — both cases simply
	// return an empty slice; Service is responsible for checking the
	// deployment itself exists first (see Service.Placements).
	ListByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]Placement, error)

	// Schedule runs one complete scheduling attempt for deploymentID inside
	// a single PostgreSQL transaction:
	//
	//  1. Locks the deployments row for deploymentID (SELECT ... FOR
	//     UPDATE). This both confirms the deployment still exists and
	//     serializes every concurrent Schedule call for the *same*
	//     deployment against every other one — two overlapping calls for
	//     deploymentID can never proceed past this step at the same time.
	//     Returns ErrDeploymentNotFound if no such row exists.
	//  2. Locks every currently-Ready node row (SELECT ... FROM nodes
	//     WHERE status = 'Ready' ORDER BY id FOR UPDATE) — always in
	//     ascending-ID order, always the complete Ready set, never a
	//     caller-chosen subset. This is what serializes concurrent
	//     Schedule calls for *different* deployments that might otherwise
	//     race to overcommit the same node: see docs/scheduling.md.
	//  3. Computes each locked node's current cluster-wide allocation —
	//     the sum of cpu/memory_bytes across every existing placement on
	//     that node, from every deployment, not just deploymentID — from
	//     inside the same transaction, so this reflects every write any
	//     other, now-completed transaction has committed, and cannot be
	//     concurrently changed by any other transaction until this one
	//     commits or rolls back (the node-row locks from step 2 guarantee
	//     that: nothing else can insert a placement on a Ready node without
	//     first taking the same lock).
	//  4. Reads deploymentID's own existing placements.
	//  5. Calls decide(nodes, existing) exactly once. decide is pure Go —
	//     no SQL, no I/O — see DecideFunc's own doc comment.
	//  6. Inserts exactly the placements decide returned, and commits.
	//
	// If decide returns an error, or step 1 or 6 fails, the transaction is
	// rolled back and zero rows are written — this is Schedule's atomicity
	// guarantee: a scheduling attempt that cannot be fully satisfied never
	// leaves a partial result behind.
	Schedule(ctx context.Context, deploymentID uuid.UUID, decide DecideFunc) ([]Placement, error)
}

// postgresRepository is the PostgreSQL-backed Repository.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository builds a Repository backed by db. The
// `deployment_placements` table must already exist — see
// internal/database's migration runner.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

const placementColumns = `id, deployment_id, replica_index, node_id, cpu, memory_bytes, created_at, updated_at`

func (r *postgresRepository) ListByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]Placement, error) {
	const query = `SELECT ` + placementColumns + ` FROM deployment_placements WHERE deployment_id = $1 ORDER BY replica_index ASC`

	rows, err := r.db.QueryContext(ctx, query, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("scheduler: listing placements for deployment %s: %w", deploymentID, err)
	}
	defer rows.Close()

	placements := []Placement{}
	for rows.Next() {
		p, err := scanPlacement(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: listing placements for deployment %s: %w", deploymentID, err)
		}
		placements = append(placements, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: listing placements for deployment %s: %w", deploymentID, err)
	}
	return placements, nil
}

func (r *postgresRepository) Schedule(ctx context.Context, deploymentID uuid.UUID, decide DecideFunc) ([]Placement, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: beginning scheduling transaction: %w", err)
	}
	// Rolling back a transaction that already committed is a documented
	// no-op (sql.ErrTxDone), so an unconditional deferred Rollback is safe
	// on every return path, including the success path after Commit.
	defer tx.Rollback()

	// Step 1: lock the deployment row. See Schedule's doc comment for why
	// this single row is the correct thing to lock to serialize concurrent
	// scheduling of the *same* deployment.
	var lockedID uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT id FROM deployments WHERE id = $1 FOR UPDATE`, deploymentID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeploymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scheduler: locking deployment %s: %w", deploymentID, err)
	}

	// Step 2: lock every Ready node, ascending by ID — the standard
	// PostgreSQL idiom for deadlock-free multi-row locking (see
	// docs/scheduling.md's "Deadlock avoidance" section). This is always
	// the complete Ready set for the whole cluster, never a subset chosen
	// ahead of time, because the scheduling algorithm needs to consider
	// every Ready node as a candidate for every replica.
	nodes, err := lockReadyNodes(ctx, tx)
	if err != nil {
		return nil, err
	}

	// Step 3: cluster-wide allocation per node, computed from inside this
	// same transaction — safe to read now precisely because step 2 already
	// holds a lock on every node a concurrent transaction could otherwise
	// be writing a new placement against.
	allocated, err := clusterWideAllocation(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		alloc := allocated[nodes[i].NodeID]
		nodes[i].AllocatedCPU = alloc.cpu
		nodes[i].AllocatedMemory = alloc.memory
	}

	// Step 4: this deployment's own existing placements.
	existing, err := listPlacementsTx(ctx, tx, deploymentID)
	if err != nil {
		return nil, err
	}

	// Step 5: the pure decision — no SQL below this point until the writes
	// in step 6.
	newPlacements, err := decide(nodes, existing)
	if err != nil {
		return nil, err
	}

	if len(newPlacements) == 0 {
		// Idempotent no-op: nothing new to place. Still commit (rather than
		// let the deferred Rollback run) to release the locks cleanly and
		// promptly instead of holding them until this connection's next
		// statement or the deferred Rollback's own round trip.
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("scheduler: committing no-op schedule for deployment %s: %w", deploymentID, err)
		}
		return sortedByReplicaIndex(existing), nil
	}

	// Step 6: persist exactly what decide returned.
	const insert = `
		INSERT INTO deployment_placements (` + placementColumns + `)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	for _, p := range newPlacements {
		if _, err := tx.ExecContext(ctx, insert,
			p.ID, p.DeploymentID, p.ReplicaIndex, p.NodeID, p.CPU, p.MemoryBytes, p.CreatedAt, p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scheduler: persisting placement for deployment %s replica %d: %w", deploymentID, p.ReplicaIndex, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("scheduler: committing schedule for deployment %s: %w", deploymentID, err)
	}

	return sortedByReplicaIndex(append(existing, newPlacements...)), nil
}

// lockReadyNodes locks and returns every currently-Ready node's identity
// and capacity, in ascending-ID order. AllocatedCPU/AllocatedMemory are
// left zero here; the caller fills them in from clusterWideAllocation.
func lockReadyNodes(ctx context.Context, tx *sql.Tx) ([]NodeState, error) {
	const query = `
		SELECT id, cpu_capacity, memory_capacity_bytes
		FROM nodes
		WHERE status = 'Ready'
		ORDER BY id ASC
		FOR UPDATE`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("scheduler: locking Ready nodes: %w", err)
	}
	defer rows.Close()

	nodes := []NodeState{}
	for rows.Next() {
		var n NodeState
		if err := rows.Scan(&n.NodeID, &n.CPUCapacity, &n.MemoryCapacity); err != nil {
			return nil, fmt.Errorf("scheduler: locking Ready nodes: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: locking Ready nodes: %w", err)
	}
	return nodes, nil
}

type allocation struct {
	cpu    int64
	memory int64
}

// clusterWideAllocation sums cpu/memory_bytes across every placement in the
// table, grouped by node, regardless of which deployment each placement
// belongs to — see Schedule's doc comment (step 3) and
// docs/scheduling.md's "Resource accounting" section for why this must be
// cluster-wide rather than scoped to the deployment being scheduled. A node
// with no placements simply has no entry in the returned map; callers treat
// a missing entry as zero allocation.
func clusterWideAllocation(ctx context.Context, tx *sql.Tx) (map[uuid.UUID]allocation, error) {
	const query = `
		SELECT node_id, COALESCE(SUM(cpu), 0), COALESCE(SUM(memory_bytes), 0)
		FROM deployment_placements
		GROUP BY node_id`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("scheduler: reading cluster-wide allocation: %w", err)
	}
	defer rows.Close()

	result := map[uuid.UUID]allocation{}
	for rows.Next() {
		var (
			nodeID uuid.UUID
			a      allocation
		)
		if err := rows.Scan(&nodeID, &a.cpu, &a.memory); err != nil {
			return nil, fmt.Errorf("scheduler: reading cluster-wide allocation: %w", err)
		}
		result[nodeID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: reading cluster-wide allocation: %w", err)
	}
	return result, nil
}

func listPlacementsTx(ctx context.Context, tx *sql.Tx, deploymentID uuid.UUID) ([]Placement, error) {
	const query = `SELECT ` + placementColumns + ` FROM deployment_placements WHERE deployment_id = $1 ORDER BY replica_index ASC`

	rows, err := tx.QueryContext(ctx, query, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("scheduler: reading existing placements for deployment %s: %w", deploymentID, err)
	}
	defer rows.Close()

	placements := []Placement{}
	for rows.Next() {
		p, err := scanPlacement(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: reading existing placements for deployment %s: %w", deploymentID, err)
		}
		placements = append(placements, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: reading existing placements for deployment %s: %w", deploymentID, err)
	}
	return placements, nil
}

func sortedByReplicaIndex(placements []Placement) []Placement {
	sort.Slice(placements, func(i, j int) bool { return placements[i].ReplicaIndex < placements[j].ReplicaIndex })
	return placements
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlacement(row rowScanner) (Placement, error) {
	var p Placement
	err := row.Scan(
		&p.ID, &p.DeploymentID, &p.ReplicaIndex, &p.NodeID, &p.CPU, &p.MemoryBytes, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return Placement{}, err
	}
	return p, nil
}
