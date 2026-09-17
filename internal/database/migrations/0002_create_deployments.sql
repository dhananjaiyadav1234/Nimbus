-- Desired workload state: one row per Deployment a user has submitted.
-- PostgreSQL is the sole source of truth, exactly as for cluster
-- membership (0001_create_nodes.sql) — there is no in-memory authoritative
-- copy anywhere in the Control Plane.
--
-- Phase 2.1 persists desired state only. There is deliberately no table
-- here for scheduling assignments, running container instances, or
-- reconciliation state — those belong to Phase 2.2 and Phase 3
-- respectively, and adding their tables now, before the components that
-- would use them exist, would be exactly the kind of premature
-- architecture this project's phases are structured to avoid.
CREATE TABLE IF NOT EXISTS deployments (
    id            UUID PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    image         TEXT NOT NULL,
    replicas      INTEGER NOT NULL CHECK (replicas >= 0),
    cpu           BIGINT NOT NULL CHECK (cpu > 0),
    memory_bytes  BIGINT NOT NULL CHECK (memory_bytes > 0),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);
