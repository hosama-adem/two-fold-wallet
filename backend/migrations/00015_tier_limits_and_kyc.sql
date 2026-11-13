-- +goose Up
-- KYC tier caps (PRD §12.1) and identity submissions (FR-KYC).
-- Daily/monthly usage is derived from postings in Africa/Addis_Ababa windows —
-- no stored counters that can drift.

CREATE TABLE tier_limits (
    tier                SMALLINT PRIMARY KEY,
    max_balance_minor   BIGINT NOT NULL,
    per_txn_minor       BIGINT NOT NULL,
    daily_minor         BIGINT NOT NULL,
    monthly_minor       BIGINT NOT NULL,
    can_withdraw        BOOLEAN NOT NULL,

    CONSTRAINT tier_limits_tier_range CHECK (tier BETWEEN 0 AND 2),
    CONSTRAINT tier_limits_max_balance_nonneg CHECK (
        max_balance_minor >= 0 AND max_balance_minor <= 1000000000000000
    ),
    CONSTRAINT tier_limits_per_txn_positive CHECK (
        per_txn_minor > 0 AND per_txn_minor <= 1000000000000000
    ),
    CONSTRAINT tier_limits_daily_positive CHECK (
        daily_minor > 0 AND daily_minor <= 1000000000000000
    ),
    CONSTRAINT tier_limits_monthly_bounds CHECK (
        monthly_minor >= daily_minor AND monthly_minor <= 1000000000000000
    )
);

-- Illustrative defaults from PRD §12.1 (ETB → minor units ×100).
INSERT INTO tier_limits (
    tier, max_balance_minor, per_txn_minor, daily_minor, monthly_minor, can_withdraw
) VALUES
    (0, 500000, 100000, 200000, 1000000, false),
    (1, 10000000, 2500000, 5000000, 30000000, true),
    (2, 100000000, 20000000, 50000000, 300000000, true);

CREATE TABLE kyc_submissions (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users (id),
    target_tier     SMALLINT NOT NULL,
    data            JSONB NOT NULL DEFAULT '{}'::jsonb,
    document_keys   TEXT[] NOT NULL DEFAULT '{}'::text[],
    status          TEXT NOT NULL DEFAULT 'submitted',
    reviewed_by     UUID,
    reviewed_at     TIMESTAMPTZ,
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT kyc_submissions_target_tier_range CHECK (target_tier BETWEEN 1 AND 2),
    CONSTRAINT kyc_submissions_status_known CHECK (
        status IN ('submitted', 'approved', 'rejected')
    ),
    CONSTRAINT kyc_submissions_review_consistent CHECK (
        (
            status = 'submitted'
            AND reviewed_by IS NULL
            AND reviewed_at IS NULL
            AND reason IS NULL
        )
        OR (
            status = 'approved'
            AND reviewed_at IS NOT NULL
        )
        OR (
            status = 'rejected'
            AND reviewed_at IS NOT NULL
            AND reason IS NOT NULL
            AND length(btrim(reason)) > 0
        )
    )
);

CREATE INDEX kyc_submissions_user_id_idx
    ON kyc_submissions (user_id, created_at DESC);

CREATE INDEX kyc_submissions_queue_idx
    ON kyc_submissions (created_at ASC)
    WHERE status = 'submitted';

-- One open submission per user. Resubmit after approve/reject creates a new row.
CREATE UNIQUE INDEX kyc_submissions_one_open_uidx
    ON kyc_submissions (user_id)
    WHERE status = 'submitted';

-- +goose Down
DROP INDEX IF EXISTS kyc_submissions_one_open_uidx;
DROP INDEX IF EXISTS kyc_submissions_queue_idx;
DROP INDEX IF EXISTS kyc_submissions_user_id_idx;
DROP TABLE IF EXISTS kyc_submissions;
DROP TABLE IF EXISTS tier_limits;
