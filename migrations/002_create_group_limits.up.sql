CREATE TABLE IF NOT EXISTS group_media_limits (
    id SERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL UNIQUE,
    chat_title VARCHAR(255),
    default_daily_limit INTEGER NOT NULL DEFAULT 0,
    timezone VARCHAR(50) NOT NULL DEFAULT 'UTC',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_group_media_limits_chat_id ON group_media_limits(chat_id);

-- Add a trigger to update the updated_at timestamp
CREATE OR REPLACE FUNCTION update_group_media_limits_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_group_media_limits_updated_at
    BEFORE UPDATE ON group_media_limits
    FOR EACH ROW
    EXECUTE FUNCTION update_group_media_limits_updated_at();