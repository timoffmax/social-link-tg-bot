-- Per-person and per-group special-download limits from the dashboard: a
-- second change-request kind the bot applies to gated_fetch_policies, and an
-- audit of every policy write (dashboard, /gated, group upgrade, psql) into
-- settings_audit. No grant changes.
ALTER TABLE settings_change_requests DROP CONSTRAINT IF EXISTS settings_change_requests_kind_check;
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_kind_check
    CHECK (kind IN ('setting', 'special_limit'));

-- A limit request names one person (id > 0) or group (id < 0); its value is a
-- whole number 0-1000 or NULL (back to the default); expected is JSON null or
-- a whole number >= -1 (the stored limit the page showed). CASE keeps the
-- casts from running on a value that is not a number.
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_special_limit_key_check
    CHECK ((kind <> 'special_limit') OR (key ~ '^(user:[1-9][0-9]{0,18}|chat:-[1-9][0-9]{0,18})$'));
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_special_limit_value_check
    CHECK (CASE
        WHEN kind <> 'special_limit' THEN true
        WHEN value IS NULL THEN true
        WHEN jsonb_typeof(value) <> 'number' THEN false
        WHEN (value #>> '{}') !~ '^[0-9]{1,4}$' THEN false
        ELSE ((value #>> '{}')::int <= 1000)
    END);
ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_special_limit_expected_check
    CHECK (CASE
        WHEN kind <> 'special_limit' THEN true
        WHEN jsonb_typeof(expected) = 'null' THEN true
        WHEN jsonb_typeof(expected) <> 'number' THEN false
        ELSE ((expected #>> '{}') ~ '^(-1|[0-9]{1,10})$')
    END);

-- The open-request index is already per (kind, key): one open request per
-- person or group, independent of the setting keys. Unchanged.

COMMENT ON TABLE gated_fetch_policies IS 'A missing row = enabled with the default limit; monthly_limit NULL = default, -1 = unlimited (only /gated), 0 = group off / person blocked';

-- One settings_audit row per changed monthly_limit (key
-- special_limit:<scope>:<id>) and per changed enabled flag (key
-- special_enabled:<scope>:<id>). A missing row counts as (enabled, NULL), so
-- inserting or deleting an all-default row logs nothing.
CREATE OR REPLACE FUNCTION gated_fetch_policies_audit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    request_setting TEXT := NULLIF(current_setting('app.settings_request_id', true), '');
    note_setting TEXT := NULLIF(current_setting('app.settings_note', true), '');
    audit_request_id BIGINT := NULL;
    old_limit JSONB := NULL;
    new_limit JSONB := NULL;
    old_enabled BOOLEAN := true;
    new_enabled BOOLEAN := true;
    row_scope TEXT;
    row_id BIGINT;
BEGIN
    IF request_setting ~ '^[0-9]{1,18}$' THEN
        audit_request_id := request_setting::BIGINT;
    END IF;

    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_limit := to_jsonb(OLD.monthly_limit);
        old_enabled := OLD.enabled;
        row_scope := OLD.scope;
        row_id := OLD.scope_id;
    END IF;

    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_limit := to_jsonb(NEW.monthly_limit);
        new_enabled := NEW.enabled;
        row_scope := NEW.scope;
        row_id := NEW.scope_id;
    END IF;

    IF old_limit IS DISTINCT FROM new_limit THEN
        INSERT INTO settings_audit (key, old_value, new_value, actor, request_id, note)
        VALUES (format('special_limit:%s:%s', row_scope, row_id), old_limit, new_limit, bot_settings_actor(), audit_request_id, note_setting);
    END IF;

    IF old_enabled IS DISTINCT FROM new_enabled THEN
        INSERT INTO settings_audit (key, old_value, new_value, actor, request_id, note)
        VALUES (format('special_enabled:%s:%s', row_scope, row_id), to_jsonb(old_enabled), to_jsonb(new_enabled), bot_settings_actor(), audit_request_id, note_setting);
    END IF;

    RETURN NULL;
END;
$$;

CREATE OR REPLACE TRIGGER gated_fetch_policies_audit AFTER INSERT OR UPDATE OR DELETE ON gated_fetch_policies
    FOR EACH ROW EXECUTE FUNCTION gated_fetch_policies_audit();

-- TRUNCATE would skip the audit.
CREATE OR REPLACE TRIGGER gated_fetch_policies_no_truncate BEFORE TRUNCATE ON gated_fetch_policies
    FOR EACH STATEMENT EXECUTE FUNCTION settings_refuse_write();
