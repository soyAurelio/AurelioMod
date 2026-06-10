-- 004_billing_counter.sql
-- Monthly analysis counters for workspace plan enforcement.

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS monthly_analysis_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS monthly_analysis_limit INTEGER NOT NULL DEFAULT 1000;
