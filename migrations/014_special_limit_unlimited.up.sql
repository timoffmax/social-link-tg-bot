-- The dashboard may set a person or group unlimited (-1), like /gated: the
-- value CHECK of a limit request also takes -1.
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_special_limit_value_check;
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_special_limit_value_check
    CHECK (CASE
        WHEN kind <> 'special_limit' THEN true
        WHEN value IS NULL THEN true
        WHEN jsonb_typeof(value) <> 'number' THEN false
        WHEN (value #>> '{}') = '-1' THEN true
        WHEN (value #>> '{}') !~ '^[0-9]{1,4}$' THEN false
        ELSE ((value #>> '{}')::int <= 1000)
    END);

COMMENT ON TABLE gated_fetch_policies IS 'A missing row = enabled with the default limit; monthly_limit NULL = default, -1 = unlimited (/gated or the dashboard), 0 = group off / person blocked';
