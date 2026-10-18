-- Ledger of paid-provider requests for a provider that has no "spent this
-- month" figure (HikerAPI: a prepaid balance). One row per request; the
-- monthly budget check sums cost_usd since the start of the UTC month.
CREATE TABLE IF NOT EXISTS paid_requests (
    id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(32) NOT NULL, -- e.g. 'hiker'
    feature VARCHAR(16) NOT NULL CHECK (feature IN ('gated', 'story')),
    status_code INTEGER NOT NULL, -- HTTP status, 0 when no answer came
    charged BOOLEAN NOT NULL, -- whether the provider bills this status
    cost_usd NUMERIC(12, 6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0), -- 0 when not charged
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_paid_requests_provider_created_at ON paid_requests (provider, created_at);
