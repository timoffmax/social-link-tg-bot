-- Back to 0-1000 or NULL: the narrow CHECK refuses unlimited requests, so
-- they go first (applied ones stay in gated_fetch_policies and the audit).
DELETE FROM settings_change_requests WHERE kind = 'special_limit' AND value = '-1'::jsonb;

ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_special_limit_value_check;
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_special_limit_value_check
    CHECK (CASE
        WHEN kind <> 'special_limit' THEN true
        WHEN value IS NULL THEN true
        WHEN jsonb_typeof(value) <> 'number' THEN false
        WHEN (value #>> '{}') !~ '^[0-9]{1,4}$' THEN false
        ELSE ((value #>> '{}')::int <= 1000)
    END);

COMMENT ON TABLE gated_fetch_policies IS 'A missing row = enabled with the default limit; monthly_limit NULL = default, -1 = unlimited (only /gated), 0 = group off / person blocked';
