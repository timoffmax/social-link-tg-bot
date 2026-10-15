-- Group boosts: a Stars payment can add bonus restricted-post downloads to a
-- group instead of the payer. The group's bonus downloads are shared by all
-- members and used after the group's monthly cap.
ALTER TABLE support_payments ADD COLUMN IF NOT EXISTS boosted_chat_id BIGINT; -- group credited by a boost; NULL for personal support

-- Bonus restricted-post credits per group, changed only by atomic updates.
CREATE TABLE IF NOT EXISTS gated_fetch_chat_credits (
    chat_id BIGINT PRIMARY KEY,
    credits INTEGER NOT NULL DEFAULT 0 CHECK (credits >= 0),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE OR REPLACE FUNCTION update_gated_fetch_chat_credits_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_gated_fetch_chat_credits_updated_at
    BEFORE UPDATE ON gated_fetch_chat_credits
    FOR EACH ROW
    EXECUTE FUNCTION update_gated_fetch_chat_credits_updated_at();
