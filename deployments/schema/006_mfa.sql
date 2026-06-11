-- 006_mfa.sql
-- Multi-Factor Authentication (TOTP) for admin access.
-- Implements RFC 6238 TOTP via pquerna/otp.
-- Recovery codes for lost-device scenarios.

CREATE TABLE IF NOT EXISTS workspace_mfa (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    totp_secret TEXT NOT NULL,           -- base32-encoded TOTP secret
    enabled BOOLEAN NOT NULL DEFAULT false,
    enrolled_at TIMESTAMPTZ,            -- when the user first set up MFA
    last_used_at TIMESTAMPTZ,           -- last successful MFA validation
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,            -- BLAKE3 hash of the recovery code
    used BOOLEAN NOT NULL DEFAULT false,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for lookup during recovery code validation
CREATE INDEX IF NOT EXISTS idx_recovery_workspace
    ON mfa_recovery_codes(workspace_id, code_hash)
    WHERE used = false;
