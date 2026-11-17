-- +goose Up
-- Money requests (FR-REQ, PRD §10.4).
-- Lifecycle: pending → paid | declined | cancelled | expired.
-- Transitions are guarded in SQL (UPDATE ... WHERE status = 'pending') so two
-- concurrent pays cannot both succeed. paid_transaction_id is UNIQUE so a
-- request can be linked to at most one transfer.

CREATE TABLE money_requests (
    id                   UUID PRIMARY KEY,
    requester_id         UUID NOT NULL REFERENCES users (id),
    payer_id             UUID NOT NULL REFERENCES users (id),
    amount_minor         BIGINT NOT NULL,
    currency             TEXT NOT NULL,
    note                 TEXT,
    status               TEXT NOT NULL DEFAULT 'pending',
    paid_transaction_id  UUID REFERENCES transactions (id),
    expires_at           TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT money_requests_not_self CHECK (requester_id <> payer_id),
    CONSTRAINT money_requests_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT money_requests_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT money_requests_status_known CHECK (
        status IN ('pending', 'paid', 'declined', 'cancelled', 'expired')
    ),
    CONSTRAINT money_requests_paid_txn_unique UNIQUE (paid_transaction_id),
    CONSTRAINT money_requests_paid_has_txn CHECK (
        (status = 'paid' AND paid_transaction_id IS NOT NULL)
        OR (status <> 'paid' AND paid_transaction_id IS NULL)
    ),
    CONSTRAINT money_requests_expires_after_create CHECK (expires_at > created_at)
);

CREATE INDEX money_requests_payer_created_idx
    ON money_requests (payer_id, created_at DESC);

CREATE INDEX money_requests_requester_created_idx
    ON money_requests (requester_id, created_at DESC);

CREATE INDEX money_requests_pending_expires_idx
    ON money_requests (expires_at)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS money_requests;
