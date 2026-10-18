-- Replies to user reports. The dashboard queues them ('queued'), the bot's
-- outbox worker sends them; /reply records the owner's replies sent from
-- Telegram. A row in 'sending' was claimed by a sender (claimed_at) and is
-- never resent automatically: one stuck there too long becomes 'unknown'
-- (may have been delivered), so a crash can not send a reply twice.
-- 'failed' is a definite refusal (or a reply that never left the bot).
-- resolve_on_send: resolve the report when the reply is delivered (set by the
-- bot in the same transaction that marks it 'sent').
-- attempts / next_attempt_at: a transient refusal (429, 5xx) puts the reply
-- back to 'queued', not to be claimed before next_attempt_at.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMP WITH TIME ZONE;

CREATE TABLE IF NOT EXISTS report_replies (
    id SERIAL PRIMARY KEY,
    report_id INTEGER NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    source VARCHAR(16) NOT NULL CHECK (source IN ('dashboard', 'telegram')),
    status VARCHAR(16) NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'unknown')),
    resolve_on_send BOOLEAN NOT NULL DEFAULT false,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE,
    claimed_at TIMESTAMP WITH TIME ZONE,
    error TEXT,
    telegram_message_id BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    sent_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_report_replies_report_id ON report_replies(report_id);
CREATE INDEX IF NOT EXISTS idx_report_replies_pending ON report_replies(created_at) WHERE status IN ('queued', 'sending');
