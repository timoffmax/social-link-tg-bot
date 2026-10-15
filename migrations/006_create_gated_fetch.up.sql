-- Paid fetch of Instagram posts gated by their author (e.g. age limits).
-- Policies: group chats are denied unless the owner enables them; private
-- chats get a monthly per-user allowance. A NULL monthly_limit means the
-- env default applies.
CREATE TABLE IF NOT EXISTS gated_fetch_policies (
    scope VARCHAR(10) NOT NULL,         -- 'chat' (group chat) or 'user' (private chat allowance)
    scope_id BIGINT NOT NULL,           -- chat id or user id
    enabled BOOLEAN NOT NULL DEFAULT false,
    monthly_limit INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope, scope_id)
);

-- Monthly usage counters, reserved atomically before each paid call.
-- A new month is a new period row, so counters reset without a cron job.
CREATE TABLE IF NOT EXISTS gated_fetch_usage (
    scope VARCHAR(10) NOT NULL,         -- 'global', 'chat' or 'user'
    scope_id BIGINT NOT NULL,           -- 0 for global
    period DATE NOT NULL,               -- first day of the month, UTC
    used INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope, scope_id, period)
);

CREATE OR REPLACE FUNCTION update_gated_fetch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_gated_fetch_policies_updated_at
    BEFORE UPDATE ON gated_fetch_policies
    FOR EACH ROW
    EXECUTE FUNCTION update_gated_fetch_updated_at();

CREATE TRIGGER update_gated_fetch_usage_updated_at
    BEFORE UPDATE ON gated_fetch_usage
    FOR EACH ROW
    EXECUTE FUNCTION update_gated_fetch_updated_at();
