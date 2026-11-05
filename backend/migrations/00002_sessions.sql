-- +goose Up
CREATE TABLE sessions (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users (id),
    family_id     UUID NOT NULL,
    refresh_hash  TEXT NOT NULL,
    user_agent    TEXT,
    ip            TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    replaced_by   UUID REFERENCES sessions (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT sessions_refresh_hash_unique UNIQUE (refresh_hash)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_family_id_idx ON sessions (family_id);
CREATE INDEX sessions_active_user_idx
    ON sessions (user_id)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS sessions_active_user_idx;
DROP INDEX IF EXISTS sessions_family_id_idx;
DROP INDEX IF EXISTS sessions_user_id_idx;
DROP TABLE IF EXISTS sessions;
