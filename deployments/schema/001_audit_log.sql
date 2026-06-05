-- 001_audit_log.sql
-- Immutable append-only audit trail for NIS2/GDPR compliance.
-- Applied via Neon DB migrations or manual psql.
--
-- Retention: 90 days (managed by Neon DB TTL policy, not SQL).
-- Index: composite on (workspace_id, timestamp_utc) for dashboard queries.

CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_id TEXT NOT NULL UNIQUE,
    workspace_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    decision TEXT NOT NULL,
    confidence REAL,
    category TEXT,
    analyst_version TEXT,
    normalization_pipeline TEXT,
    processing_ms INTEGER,
    timestamp_utc TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_workspace_time
    ON audit_log(workspace_id, timestamp_utc);
