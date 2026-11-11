-- +goose Up
-- HTTP idempotency ledger (PRD §9).
-- Same (user_id, key) + same request hash replays the stored response.
-- Same key + different body returns 422. Concurrent claim returns 409 while in_progress.
-- Keys expire after 24 hours. DeleteExpired (Day 25 job) removes them.
-- An expired row may be reclaimed on the next claim (see ClaimKey).

CREATE TABLE idempotency_keys (
    user_id                UUID NOT NULL REFERENCES users (id),
    key                    TEXT NOT NULL,
    request_hash           TEXT NOT NULL,
    state                  TEXT NOT NULL,
    response_code          INTEGER,
    response_body          BYTEA,
    response_content_type  TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at             TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (user_id, key),

    CONSTRAINT idempotency_keys_key_uuid_v4 CHECK (
        key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT idempotency_keys_request_hash_sha256 CHECK (
        request_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT idempotency_keys_state_known CHECK (
        state IN ('in_progress', 'completed')
    ),
    CONSTRAINT idempotency_keys_response_consistent CHECK (
        (
            state = 'in_progress'
            AND response_code IS NULL
            AND response_body IS NULL
            AND response_content_type IS NULL
        )
        OR (
            state = 'completed'
            AND response_code IS NOT NULL
            AND response_code BETWEEN 100 AND 599
            AND response_body IS NOT NULL
            AND response_content_type IS NOT NULL
            AND length(btrim(response_content_type)) BETWEEN 1 AND 200
        )
    ),
    CONSTRAINT idempotency_keys_expires_after_created CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idempotency_keys_expires_at_idx;
DROP TABLE IF EXISTS idempotency_keys;
