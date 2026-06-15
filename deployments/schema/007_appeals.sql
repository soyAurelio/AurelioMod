-- 007_appeals.sql
-- GDPR Art. 22 — Appeals for automated decisions.
-- Allows users to request human review of moderation decisions.
-- Applied via Neon DB migrations or manual psql.

CREATE TABLE IF NOT EXISTS workspace_appeals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    audit_id TEXT NOT NULL REFERENCES audit_log(audit_id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    contact_email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'under_review', 'upheld', 'overturned')),
    reviewer_notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One appeal per decision per workspace (idempotent)
CREATE UNIQUE INDEX IF NOT EXISTS idx_appeals_workspace_audit
    ON workspace_appeals(workspace_id, audit_id);

CREATE INDEX IF NOT EXISTS idx_appeals_status
    ON workspace_appeals(status, created_at);
