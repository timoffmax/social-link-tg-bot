DROP TRIGGER IF EXISTS gated_fetch_policies_no_truncate ON gated_fetch_policies;
DROP TRIGGER IF EXISTS gated_fetch_policies_audit ON gated_fetch_policies;
DROP FUNCTION IF EXISTS gated_fetch_policies_audit();
COMMENT ON TABLE gated_fetch_policies IS NULL;
-- The narrow CHECK refuses these rows, so they go first; applied changes stay
-- in gated_fetch_policies and in the append-only audit.
DELETE FROM settings_change_requests WHERE kind = 'special_limit';
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_special_limit_expected_check;
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_special_limit_value_check;
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_special_limit_key_check;
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_kind_check;
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_kind_check CHECK (kind IN ('setting'));
