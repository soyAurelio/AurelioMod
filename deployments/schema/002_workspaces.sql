-- 002_workspaces.sql
-- AurelioMod workspace management.
-- Each customer (Discord server, agency, etc.) gets one workspace.
--
-- api_key is used for auth: POST /v1/auth/login with api_key → PASETO token.
-- plan maps to WaveSpeed concurrency tiers (bronze=3, silver=100, gold=2000, ultra=5000).

CREATE TABLE IF NOT EXISTS workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    api_key TEXT NOT NULL UNIQUE,
    plan TEXT NOT NULL DEFAULT 'bronze',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
