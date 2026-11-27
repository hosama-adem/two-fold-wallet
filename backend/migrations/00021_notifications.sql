-- +goose Up
-- In-app notifications produced from outbox events (FR-NOT, PRD §6.11).
-- UNIQUE (user_id, outbox_event_id) makes the consumer idempotent under
-- at-least-once River delivery.

CREATE TABLE notifications (
    id               UUID PRIMARY KEY,
    user_id          UUID NOT NULL REFERENCES users (id),
    outbox_event_id  UUID NOT NULL REFERENCES outbox_events (id),
    topic            TEXT NOT NULL,
    title            TEXT NOT NULL,
    body             TEXT NOT NULL,
    data             JSONB NOT NULL DEFAULT '{}'::jsonb,
    read_at          TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT notifications_user_event_unique UNIQUE (user_id, outbox_event_id),
    CONSTRAINT notifications_topic_present CHECK (length(btrim(topic)) BETWEEN 1 AND 128),
    CONSTRAINT notifications_title_present CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    CONSTRAINT notifications_body_present CHECK (length(btrim(body)) BETWEEN 1 AND 1000)
);

CREATE INDEX notifications_user_created_idx
    ON notifications (user_id, created_at DESC, id DESC);

CREATE INDEX notifications_user_unread_idx
    ON notifications (user_id, created_at DESC)
    WHERE read_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS notifications;
