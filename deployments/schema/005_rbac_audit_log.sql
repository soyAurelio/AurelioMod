-- 005_rbac_audit_log.sql
-- Enforces append-only immutability on audit_log at the database level.
-- Layer 1: REVOKE UPDATE/DELETE/TRUNCATE from public and all roles.
-- Layer 2: Trigger that raises an exception on any non-INSERT operation.
-- Layer 3: Row-level security (RLS) — only audit_writer can INSERT.
--
-- Applied via Neon DB migrations or manual psql.
-- Run as superuser/table owner.

-- ============================================================
-- Layer 1: Revoke destructive permissions from PUBLIC
-- ============================================================
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM PUBLIC;

-- ============================================================
-- Layer 2: Create audit_writer role with minimal privileges
-- ============================================================
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_writer') THEN
        CREATE ROLE audit_writer WITH LOGIN PASSWORD NULL; -- password set via env/secret
    END IF;
END
$$;

-- Grant only INSERT and SELECT (read for verification, no modify)
GRANT INSERT, SELECT ON audit_log TO audit_writer;
-- Explicitly deny UPDATE, DELETE, TRUNCATE even if granted elsewhere
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM audit_writer;

-- ============================================================
-- Layer 3: Trigger — block any non-INSERT at DB level
-- ============================================================
CREATE OR REPLACE FUNCTION enforce_audit_append_only()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'audit_log is append-only: % operation rejected on table %',
            TG_OP, TG_TABLE_NAME;
    END IF;
    RETURN NULL; -- AFTER trigger, result ignored
END;
$$;

-- Drop existing trigger if present (idempotent migration)
DROP TRIGGER IF EXISTS trg_audit_append_only ON audit_log;

CREATE TRIGGER trg_audit_append_only
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_append_only();

-- Also block TRUNCATE (statement-level)
DROP TRIGGER IF EXISTS trg_audit_no_truncate ON audit_log;

CREATE TRIGGER trg_audit_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT
    EXECUTE FUNCTION enforce_audit_append_only();

-- ============================================================
-- Layer 4: Row-Level Security (RLS)
-- ============================================================
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;

-- Drop existing policies to allow idempotent migration
DROP POLICY IF EXISTS audit_log_insert_policy ON audit_log;
DROP POLICY IF EXISTS audit_log_select_policy ON audit_log;

-- Only audit_writer can insert new audit events
CREATE POLICY audit_log_insert_policy ON audit_log
    FOR INSERT
    TO audit_writer
    WITH CHECK (true);

-- Everyone can read audit_log (transparency for compliance audits)
CREATE POLICY audit_log_select_policy ON audit_log
    FOR SELECT
    TO PUBLIC
    USING (true);

-- ============================================================
-- Verification queries (run after migration to confirm):
-- ============================================================
-- SELECT rolname, rolcanlogin FROM pg_roles WHERE rolname = 'audit_writer';
-- SELECT grantee, privilege_type FROM information_schema.role_table_grants
--   WHERE table_name = 'audit_log' ORDER BY grantee, privilege_type;
-- -- Should show: audit_writer = INSERT, SELECT only.
-- -- Should NOT show: UPDATE, DELETE, TRUNCATE for any role.
