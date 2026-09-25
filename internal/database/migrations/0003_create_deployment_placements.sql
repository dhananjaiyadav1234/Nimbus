-- Placement decisions: which node each deployment replica has been
-- assigned to by the scheduler, and the resource reservation that
-- assignment represents. PostgreSQL is the sole source of truth, exactly
-- as for nodes (0001) and deployments (0002) — there is no in-memory
-- authoritative placement state anywhere in the Control Plane.
--
-- Phase 2.2 persists placement decisions only. There is deliberately no
-- column or table here for a container ID, container status, or actual
-- running state — the scheduler produces a plan, it does not execute one.
-- Creating a container from a placement is a later phase's Node
-- Agent/Docker boundary, deliberately left unconnected here; see
-- docs/scheduling.md.
CREATE TABLE IF NOT EXISTS deployment_placements (
    id             UUID PRIMARY KEY,
    deployment_id  UUID NOT NULL,
    replica_index  INTEGER NOT NULL,
    node_id        UUID NOT NULL,
    cpu            BIGINT NOT NULL,
    memory_bytes   BIGINT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,

    FOREIGN KEY (deployment_id)
        REFERENCES deployments(id)
        ON DELETE CASCADE,

    FOREIGN KEY (node_id)
        REFERENCES nodes(id),

    CHECK (replica_index >= 0),

    CHECK (cpu > 0),

    CHECK (memory_bytes > 0),

    -- The core idempotency guarantee: a given deployment can have at most
    -- one placement per replica index. Scheduling the same deployment
    -- twice can never produce duplicate rows for the same replica —
    -- enforced by PostgreSQL, not application logic, exactly like
    -- deployments.name's UNIQUE constraint in 0002. See
    -- internal/scheduler/repository.go for how the scheduler's own
    -- row-locking strategy keeps this constraint from ever actually being
    -- raced in practice, and relies on it only as a last-resort guard.
    UNIQUE (deployment_id, replica_index)
);

-- Every placement lookup Nimbus performs today is scoped to one deployment
-- (GET /deployments/{id}/placements) or aggregates by node (the
-- scheduler's own cluster-wide resource accounting) — both are covered by
-- these two indexes.
CREATE INDEX IF NOT EXISTS idx_deployment_placements_deployment_id
    ON deployment_placements(deployment_id);

CREATE INDEX IF NOT EXISTS idx_deployment_placements_node_id
    ON deployment_placements(node_id);
