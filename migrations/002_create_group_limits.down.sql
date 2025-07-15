DROP TRIGGER IF EXISTS update_group_media_limits_updated_at ON group_media_limits;
DROP FUNCTION IF EXISTS update_group_media_limits_updated_at();
DROP INDEX IF EXISTS idx_group_media_limits_chat_id;
DROP TABLE IF EXISTS group_media_limits;