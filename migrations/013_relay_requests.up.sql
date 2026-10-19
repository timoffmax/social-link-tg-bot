-- One row per call the bot sent to the home relay, for the dashboard's relay
-- history: the platform, the outcome code ('OK', the relay's error code, or
-- TRANSPORT / REJECTED / SKIPPED for bot-side outcomes), the bytes of a
-- delivered video and the call's duration. No user, chat or video ids.
-- Rows older than 90 days are pruned by the bot once a day.
CREATE TABLE IF NOT EXISTS relay_requests (
    id BIGSERIAL PRIMARY KEY,
    platform TEXT NOT NULL,
    code TEXT NOT NULL,
    bytes BIGINT,
    duration_ms INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_relay_requests_created_at ON relay_requests (created_at);
CREATE INDEX IF NOT EXISTS idx_relay_requests_platform_created_at ON relay_requests (platform, created_at);
