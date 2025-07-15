CREATE TABLE IF NOT EXISTS media_stats (
    id SERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    username VARCHAR(255),
    chat_id BIGINT NOT NULL,
    chat_title VARCHAR(255),
    platform VARCHAR(50) NOT NULL,
    media_type VARCHAR(20) NOT NULL, -- 'photo' or 'video'
    url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for efficient queries
CREATE INDEX idx_user_chat ON media_stats (user_id, chat_id);
CREATE INDEX idx_created_at ON media_stats (created_at);
CREATE INDEX idx_platform ON media_stats (platform);
CREATE INDEX idx_stats_date ON media_stats (DATE(created_at AT TIME ZONE 'UTC'));

-- Table for user media limits (optional feature)
CREATE TABLE IF NOT EXISTS user_media_limits (
    id SERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    daily_limit INT NOT NULL DEFAULT 0, -- 0 means no limit
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(chat_id, user_id)
);