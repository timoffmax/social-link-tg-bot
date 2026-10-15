-- Optional Telegram Stars "support the developer" payments. Each payment adds
-- bonus credits for restricted-post downloads; credits don't expire monthly
-- and are used only after the monthly allowance (or chat cap) runs out.
CREATE TABLE IF NOT EXISTS support_payments (
    telegram_payment_charge_id VARCHAR(255) PRIMARY KEY, -- Telegram's charge id; makes delivery idempotent, needed for refunds
    user_id BIGINT NOT NULL,                             -- payer
    chat_id BIGINT NOT NULL,                             -- chat the payment was made in
    stars INTEGER NOT NULL,                              -- amount paid, in Stars
    credits INTEGER NOT NULL,                            -- bonus credits granted
    payload VARCHAR(128) NOT NULL,                       -- invoice payload ("support:<stars>:<user_id>")
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    refunded_at TIMESTAMP WITH TIME ZONE                 -- set when the owner refunds the payment
);

CREATE INDEX IF NOT EXISTS idx_support_payments_user_id ON support_payments (user_id);

-- Bonus restricted-post credits per user, changed only by atomic updates.
CREATE TABLE IF NOT EXISTS gated_fetch_credits (
    user_id BIGINT PRIMARY KEY,
    credits INTEGER NOT NULL DEFAULT 0 CHECK (credits >= 0),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE OR REPLACE FUNCTION update_gated_fetch_credits_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_gated_fetch_credits_updated_at
    BEFORE UPDATE ON gated_fetch_credits
    FOR EACH ROW
    EXECUTE FUNCTION update_gated_fetch_credits_updated_at();
