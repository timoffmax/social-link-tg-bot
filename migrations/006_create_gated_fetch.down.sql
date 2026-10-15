DROP TRIGGER IF EXISTS update_gated_fetch_usage_updated_at ON gated_fetch_usage;
DROP TRIGGER IF EXISTS update_gated_fetch_policies_updated_at ON gated_fetch_policies;
DROP FUNCTION IF EXISTS update_gated_fetch_updated_at();
DROP TABLE IF EXISTS gated_fetch_usage;
DROP TABLE IF EXISTS gated_fetch_policies;
