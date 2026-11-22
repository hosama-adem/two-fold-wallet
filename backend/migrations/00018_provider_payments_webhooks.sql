-- +goose Up
-- Provider collections/payouts and inbound webhook dedupe (FR-TOP, PRD §11.3).
-- Top-ups stay pending until a verified webhook (or Day-25 poll) settles them.
-- webhook_events PK (provider, event_id) makes duplicate deliveries a no-op.

CREATE TABLE provider_payments (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL REFERENCES transactions (id),
    provider         TEXT NOT NULL,
    direction        TEXT NOT NULL,
    provider_ref     TEXT NOT NULL,
    status           TEXT NOT NULL,
    amount_minor     BIGINT NOT NULL,
    currency         TEXT NOT NULL,
    raw              JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT provider_payments_provider_known CHECK (
        provider IN ('telebirr', 'chapa', 'simulator')
    ),
    CONSTRAINT provider_payments_direction_known CHECK (
        direction IN ('collection', 'payout')
    ),
    CONSTRAINT provider_payments_status_known CHECK (
        status IN ('pending', 'succeeded', 'failed', 'expired')
    ),
    CONSTRAINT provider_payments_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT provider_payments_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT provider_payments_provider_ref_present CHECK (
        length(btrim(provider_ref)) > 0
    ),
    CONSTRAINT provider_payments_provider_ref_unique UNIQUE (provider, provider_ref),
    CONSTRAINT provider_payments_transaction_unique UNIQUE (transaction_id)
);

CREATE INDEX provider_payments_status_created_idx
    ON provider_payments (status, created_at)
    WHERE status = 'pending';

CREATE INDEX provider_payments_provider_ref_idx
    ON provider_payments (provider_ref);

CREATE TABLE webhook_events (
    provider     TEXT NOT NULL,
    event_id     TEXT NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload_hash TEXT NOT NULL,
    transaction_id UUID REFERENCES transactions (id),

    PRIMARY KEY (provider, event_id),

    CONSTRAINT webhook_events_provider_known CHECK (
        provider IN ('telebirr', 'chapa', 'simulator')
    ),
    CONSTRAINT webhook_events_event_id_present CHECK (length(btrim(event_id)) > 0),
    CONSTRAINT webhook_events_payload_hash_sha256 CHECK (
        payload_hash ~ '^[0-9a-f]{64}$'
    )
);

CREATE INDEX webhook_events_received_at_idx ON webhook_events (received_at DESC);

-- +goose Down
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS provider_payments;
