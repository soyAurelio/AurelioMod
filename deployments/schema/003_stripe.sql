-- 003_stripe.sql
-- Stripe billing columns for workspace subscriptions.
-- Applied via control service auto-migration (cmd/control/main.go).

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT;
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT;
