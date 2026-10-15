DROP TRIGGER IF EXISTS update_gated_fetch_chat_credits_updated_at ON gated_fetch_chat_credits;
DROP FUNCTION IF EXISTS update_gated_fetch_chat_credits_updated_at();
DROP TABLE IF EXISTS gated_fetch_chat_credits;
ALTER TABLE support_payments DROP COLUMN IF EXISTS boosted_chat_id;
