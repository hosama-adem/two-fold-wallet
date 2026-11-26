-- +goose Up
-- Transactional outbox (PRD §11.1). Business txs insert rows here in the same
-- commit as money moves. A dispatcher claims undispatched rows with
-- FOR UPDATE SKIP LOCKED and publishes them to River.

CREATE TABLE outbox_events (
    id             UUID PRIMARY KEY,
    topic          TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at  TIMESTAMPTZ,

    CONSTRAINT outbox_events_topic_present CHECK (length(btrim(topic)) BETWEEN 1 AND 128)
);

CREATE INDEX outbox_events_undispatched_created_idx
    ON outbox_events (created_at ASC, id ASC)
    WHERE dispatched_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
