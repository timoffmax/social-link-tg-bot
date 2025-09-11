DROP TRIGGER IF EXISTS update_chat_settings_updated_at ON chat_settings;
DROP FUNCTION IF EXISTS update_chat_settings_updated_at();
DROP INDEX IF EXISTS idx_chat_settings_enabled;
DROP INDEX IF EXISTS idx_chat_settings_key;
DROP INDEX IF EXISTS idx_chat_settings_type;
DROP INDEX IF EXISTS idx_chat_settings_chat_id;
DROP TABLE IF EXISTS chat_settings;