-- Drops the settings tables: the bot then uses .env and the defaults again.
-- Run only when the feature is abandoned; a rollback is deploying the
-- previous commit, which ignores these tables.
DROP TABLE IF EXISTS bot_runtime_status;
DROP TABLE IF EXISTS settings_change_requests;
DROP TABLE IF EXISTS bot_settings;
DROP TABLE IF EXISTS settings_audit;
DROP FUNCTION IF EXISTS settings_refuse_write();
DROP FUNCTION IF EXISTS bot_settings_audit();
DROP FUNCTION IF EXISTS bot_settings_stamp();
DROP FUNCTION IF EXISTS bot_settings_actor();
