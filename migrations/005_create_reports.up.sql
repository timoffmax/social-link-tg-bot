CREATE TABLE IF NOT EXISTS reports (
    id SERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    username VARCHAR(255),
    first_name VARCHAR(255),
    chat_id BIGINT NOT NULL,
    chat_title VARCHAR(255),
    message TEXT NOT NULL,
    resolved BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reports_user_id ON reports(user_id);
CREATE INDEX IF NOT EXISTS idx_reports_resolved ON reports(resolved);
CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at);
