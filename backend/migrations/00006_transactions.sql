-- +goose Up
-- The business record a customer sees, plus the status trail.
-- Wallet foreign keys wait for the wallets table. The columns are here so a
-- transfer can name both ends. They cannot name the same id.
-- Idempotency keys are UUID-shaped and unique per initiator (PRD §9).
-- Money-request statuses (paid, declined) live on money_requests, not here.

CREATE TABLE transactions (
    id                 UUID PRIMARY KEY,
    reference          TEXT NOT NULL,
    kind               TEXT NOT NULL,
    status             TEXT NOT NULL,
    initiator_user_id  UUID REFERENCES users (id),
    from_wallet_id     UUID,
    to_wallet_id       UUID,
    amount_minor       BIGINT NOT NULL,
    fee_minor          BIGINT NOT NULL DEFAULT 0,
    currency           TEXT NOT NULL,
    note               TEXT,
    idempotency_key    TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ,

    CONSTRAINT transactions_reference_unique UNIQUE (reference),
    CONSTRAINT transactions_reference_present CHECK (length(btrim(reference)) > 0),
    CONSTRAINT transactions_kind_known CHECK (
        kind IN (
            'p2p',
            'top_up',
            'cash_out',
            'merchant_payment',
            'money_request',
            'adjustment'
        )
    ),
    CONSTRAINT transactions_status_known CHECK (
        status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transactions_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    -- 10^15 santim is the overflow ceiling (PRD §7.6). A zero amount is not a movement.
    CONSTRAINT transactions_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT transactions_fee_nonneg CHECK (
        fee_minor >= 0 AND fee_minor <= 1000000000000000
    ),
    CONSTRAINT transactions_wallets_distinct CHECK (
        from_wallet_id IS NULL
        OR to_wallet_id IS NULL
        OR from_wallet_id <> to_wallet_id
    ),
    CONSTRAINT transactions_idempotency_key_uuid CHECK (
        idempotency_key IS NULL
        OR idempotency_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    )
);

CREATE UNIQUE INDEX transactions_user_idempotency_key_idx
    ON transactions (initiator_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND initiator_user_id IS NOT NULL;

CREATE INDEX transactions_initiator_user_id_idx ON transactions (initiator_user_id);
CREATE INDEX transactions_status_idx ON transactions (status);
CREATE INDEX transactions_created_at_idx ON transactions (created_at DESC);

-- One row per status change. The current status stays on transactions.
-- Append-only triggers follow the journal immutability work.
CREATE TABLE transaction_events (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL REFERENCES transactions (id),
    from_status      TEXT NOT NULL,
    to_status        TEXT NOT NULL,
    reason           TEXT,
    actor            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transaction_events_from_status_known CHECK (
        from_status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transaction_events_to_status_known CHECK (
        to_status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transaction_events_status_moves CHECK (from_status <> to_status),
    CONSTRAINT transaction_events_actor_known CHECK (
        actor IN ('user', 'staff', 'system', 'provider')
    )
);

CREATE INDEX transaction_events_transaction_id_idx
    ON transaction_events (transaction_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS transaction_events_transaction_id_idx;
DROP TABLE IF EXISTS transaction_events;

DROP INDEX IF EXISTS transactions_created_at_idx;
DROP INDEX IF EXISTS transactions_status_idx;
DROP INDEX IF EXISTS transactions_initiator_user_id_idx;
DROP INDEX IF EXISTS transactions_user_idempotency_key_idx;
DROP TABLE IF EXISTS transactions;
