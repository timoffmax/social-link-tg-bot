-- Flexible chat settings table to handle various types of settings
CREATE TABLE IF NOT EXISTS chat_settings (
    id SERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    setting_type VARCHAR(50) NOT NULL,  -- e.g., 'platform', 'notification', 'feature'
    setting_key VARCHAR(50) NOT NULL,   -- e.g., 'instagram', 'silent_mode', 'auto_delete'
    setting_value TEXT NOT NULL,        -- JSON or simple value
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(chat_id, setting_type, setting_key)
);

CREATE INDEX IF NOT EXISTS idx_chat_settings_chat_id ON chat_settings(chat_id);
CREATE INDEX IF NOT EXISTS idx_chat_settings_type ON chat_settings(setting_type);
CREATE INDEX IF NOT EXISTS idx_chat_settings_key ON chat_settings(setting_key);
CREATE INDEX IF NOT EXISTS idx_chat_settings_enabled ON chat_settings(enabled);

-- Add a trigger to update the updated_at timestamp
CREATE OR REPLACE FUNCTION update_chat_settings_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_chat_settings_updated_at
    BEFORE UPDATE ON chat_settings
    FOR EACH ROW
    EXECUTE FUNCTION update_chat_settings_updated_at();

-- Insert default enabled platforms for existing chats (if any)
INSERT INTO chat_settings (chat_id, setting_type, setting_key, setting_value, enabled)
SELECT DISTINCT gml.chat_id, 'platform', platform.name, 'true', true
FROM group_media_limits gml
CROSS JOIN (
    VALUES 
    ('instagram'),
    ('tiktok'), 
    ('twitter'),
    ('youtube'),
    ('reddit'),
    ('facebook')
) AS platform(name)
ON CONFLICT (chat_id, setting_type, setting_key) DO NOTHING;