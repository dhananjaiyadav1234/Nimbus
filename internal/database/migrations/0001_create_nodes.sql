-- Cluster membership: one row per node that has ever registered with the
-- Control Plane. PostgreSQL is the sole source of truth for membership;
-- there is no authoritative in-memory copy.
CREATE TABLE IF NOT EXISTS nodes (
    id                     UUID PRIMARY KEY,
    hostname               TEXT NOT NULL,
    status                 TEXT NOT NULL CHECK (status IN ('Registering', 'Ready', 'NotReady')),
    os                     TEXT NOT NULL,
    architecture           TEXT NOT NULL,
    cpu_capacity           BIGINT NOT NULL CHECK (cpu_capacity > 0),
    memory_capacity_bytes  BIGINT NOT NULL CHECK (memory_capacity_bytes > 0),
    agent_version          TEXT NOT NULL,
    last_heartbeat_at      TIMESTAMPTZ,
    registered_at          TIMESTAMPTZ NOT NULL,
    updated_at             TIMESTAMPTZ NOT NULL
);

-- Serves the failure-detection sweep (`WHERE status = 'Ready' AND
-- last_heartbeat_at < cutoff`) and the readiness-style membership queries
-- that will follow it. The table is expected to stay small (one row per
-- physical/simulated node), so this single composite index is enough —
-- there is no case for indexing every column.
CREATE INDEX IF NOT EXISTS idx_nodes_status_last_heartbeat ON nodes (status, last_heartbeat_at);
