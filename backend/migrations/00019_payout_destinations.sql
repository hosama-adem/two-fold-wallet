-- +goose Up
-- Saved cash-out destinations (FR-WDR-1). The full account/phone is AES-GCM
-- encrypted at rest; APIs only ever return identifier_masked.

CREATE TABLE payout_destinations (
    id                      UUID PRIMARY KEY,
    user_id                 UUID NOT NULL REFERENCES users (id),
    provider                TEXT NOT NULL,
    kind                    TEXT NOT NULL,
    label                   TEXT NOT NULL,
    identifier_ciphertext   BYTEA NOT NULL,
    identifier_nonce        BYTEA NOT NULL,
    identifier_masked       TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at              TIMESTAMPTZ,

    CONSTRAINT payout_destinations_provider_known CHECK (
        provider IN ('telebirr', 'chapa', 'simulator')
    ),
    CONSTRAINT payout_destinations_kind_known CHECK (
        kind IN ('mobile_money', 'bank_account')
    ),
    CONSTRAINT payout_destinations_label_present CHECK (
        length(btrim(label)) BETWEEN 1 AND 80
    ),
    CONSTRAINT payout_destinations_masked_present CHECK (
        length(btrim(identifier_masked)) BETWEEN 1 AND 64
    ),
    CONSTRAINT payout_destinations_nonce_len CHECK (octet_length(identifier_nonce) = 12),
    CONSTRAINT payout_destinations_cipher_present CHECK (octet_length(identifier_ciphertext) > 0)
);

CREATE INDEX payout_destinations_user_created_idx
    ON payout_destinations (user_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- Link payouts back to the destination and the reserve journal so settle/refund
-- can find the exact compensating entry without scanning postings.
ALTER TABLE provider_payments
    ADD COLUMN destination_id UUID REFERENCES payout_destinations (id),
    ADD COLUMN reserve_journal_id UUID REFERENCES journal_entries (id);

CREATE INDEX provider_payments_destination_id_idx
    ON provider_payments (destination_id)
    WHERE destination_id IS NOT NULL;

-- +goose Down
ALTER TABLE provider_payments
    DROP COLUMN IF EXISTS reserve_journal_id,
    DROP COLUMN IF EXISTS destination_id;

DROP TABLE IF EXISTS payout_destinations;
