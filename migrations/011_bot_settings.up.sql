-- Owner-managed bot settings (dashboard Settings tab). A missing row means
-- the bot uses .env, then the code default, so this migration changes
-- nothing on its own: the tables start empty and nothing is seeded.

-- Values the owner set, validated by the bot. Written only by the bot role
-- (it applies change requests); manual psql edits are possible and audited.
-- updated_by and updated_at are set by a trigger from the writing
-- transaction's app.settings_actor ('manual' when unset).
CREATE TABLE IF NOT EXISTS bot_settings (
    key TEXT PRIMARY KEY CHECK (key ~ '^[a-z0-9_]+(\.[a-z0-9_]+)+$'),
    value JSONB NOT NULL CHECK (pg_column_size(value) <= 2048),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_by VARCHAR(16) NOT NULL DEFAULT 'manual' CHECK (updated_by IN ('dashboard', 'telegram', 'bot', 'manual'))
);

-- Append-only history of bot_settings, filled by a trigger for every write,
-- whoever made it. actor comes from app.settings_actor ('manual' when unset);
-- db_role is current_user, which the writer can not forge. old_value NULL =
-- was unset; new_value NULL = reset to .env / default.
CREATE TABLE IF NOT EXISTS settings_audit (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    old_value JSONB,
    new_value JSONB,
    actor VARCHAR(16) NOT NULL,
    db_role TEXT NOT NULL DEFAULT current_user,
    request_id BIGINT,
    note TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_settings_audit_key_created_at ON settings_audit (key, created_at);

-- The actor of the writing transaction (SET LOCAL app.settings_actor), or
-- 'manual' when unset or unknown.
CREATE OR REPLACE FUNCTION bot_settings_actor() RETURNS VARCHAR(16)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    actor TEXT := NULLIF(current_setting('app.settings_actor', true), '');
BEGIN
    IF (actor IS NULL) OR (actor NOT IN ('dashboard', 'telegram', 'bot', 'manual')) THEN
        actor := 'manual';
    END IF;

    RETURN actor;
END;
$$;

-- Stamps updated_by / updated_at and refuses renaming a key.
CREATE OR REPLACE FUNCTION bot_settings_stamp() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (TG_OP = 'UPDATE') AND (NEW.key <> OLD.key) THEN
        RAISE EXCEPTION 'bot_settings keys can not be renamed; delete and insert instead';
    END IF;

    NEW.updated_by := bot_settings_actor();
    NEW.updated_at := now();

    RETURN NEW;
END;
$$;

-- Writes one settings_audit row per changed bot_settings row.
CREATE OR REPLACE FUNCTION bot_settings_audit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    request_setting TEXT := NULLIF(current_setting('app.settings_request_id', true), '');
    note_setting TEXT := NULLIF(current_setting('app.settings_note', true), '');
    audit_request_id BIGINT := NULL;
BEGIN
    IF request_setting ~ '^[0-9]{1,18}$' THEN
        audit_request_id := request_setting::BIGINT;
    END IF;

    IF TG_OP = 'INSERT' THEN
        INSERT INTO settings_audit (key, old_value, new_value, actor, request_id, note)
        VALUES (NEW.key, NULL, NEW.value, bot_settings_actor(), audit_request_id, note_setting);
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO settings_audit (key, old_value, new_value, actor, request_id, note)
        VALUES (NEW.key, OLD.value, NEW.value, bot_settings_actor(), audit_request_id, note_setting);
    ELSE
        INSERT INTO settings_audit (key, old_value, new_value, actor, request_id, note)
        VALUES (OLD.key, OLD.value, NULL, bot_settings_actor(), audit_request_id, note_setting);
    END IF;

    RETURN NULL;
END;
$$;

-- Refuses a write that would bypass or rewrite the audit.
CREATE OR REPLACE FUNCTION settings_refuse_write() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% on % is not allowed (append-only / audited table)', TG_OP, TG_TABLE_NAME;
END;
$$;

CREATE OR REPLACE TRIGGER bot_settings_stamp BEFORE INSERT OR UPDATE ON bot_settings
    FOR EACH ROW EXECUTE FUNCTION bot_settings_stamp();

CREATE OR REPLACE TRIGGER bot_settings_audit AFTER INSERT OR UPDATE OR DELETE ON bot_settings
    FOR EACH ROW EXECUTE FUNCTION bot_settings_audit();

-- TRUNCATE would skip the row trigger, so it is refused: use DELETE.
CREATE OR REPLACE TRIGGER bot_settings_no_truncate BEFORE TRUNCATE ON bot_settings
    FOR EACH STATEMENT EXECUTE FUNCTION settings_refuse_write();

CREATE OR REPLACE TRIGGER settings_audit_append_only BEFORE UPDATE OR DELETE ON settings_audit
    FOR EACH ROW EXECUTE FUNCTION settings_refuse_write();

CREATE OR REPLACE TRIGGER settings_audit_no_truncate BEFORE TRUNCATE ON settings_audit
    FOR EACH STATEMENT EXECUTE FUNCTION settings_refuse_write();

-- Change requests: the dashboard inserts (kind, key, value, expected) only;
-- every other column has a default or is set by the bot, which validates and
-- applies them. value NULL = reset to .env / default. expected is the
-- effective value the page showed: the bot applies a request only while the
-- current value still equals it. A queued request expires 10 minutes after
-- it was made (the bot sets a new expiry when it waits for approval). One
-- open request per key, enforced at INSERT.
CREATE TABLE IF NOT EXISTS settings_change_requests (
    id BIGSERIAL PRIMARY KEY,
    kind VARCHAR(16) NOT NULL DEFAULT 'setting' CHECK (kind IN ('setting')),
    key TEXT NOT NULL CHECK (char_length(key) <= 100),
    value JSONB CHECK (pg_column_size(value) <= 2048),
    expected JSONB NOT NULL CHECK (pg_column_size(expected) <= 2048),
    source VARCHAR(16) NOT NULL DEFAULT 'dashboard' CHECK (source IN ('dashboard', 'telegram')),
    requested_role TEXT NOT NULL DEFAULT current_user,
    status VARCHAR(20) NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'awaiting_approval', 'applied', 'rejected', 'expired')),
    needs_approval BOOLEAN,
    error TEXT,
    approval_message_id BIGINT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (now() + INTERVAL '10 minutes'),
    decided_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_change_requests_one_open ON settings_change_requests (kind, key) WHERE status IN ('queued', 'awaiting_approval');
CREATE INDEX IF NOT EXISTS idx_settings_change_requests_queued ON settings_change_requests (created_at) WHERE status = 'queued';

-- What the bot reports about itself, one JSON blob per component: 'bot'
-- (started_at, commit, settings load), 'secrets' (booleans and counts only),
-- 'settings' (effective value and source per key), 'registry' (the settings
-- metadata), 'profile' (last Telegram profile push).
CREATE TABLE IF NOT EXISTS bot_runtime_status (
    component VARCHAR(32) PRIMARY KEY,
    data JSONB NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);
