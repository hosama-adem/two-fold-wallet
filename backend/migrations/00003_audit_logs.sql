-- +goose Up
CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY,
    actor_user_id   UUID REFERENCES users (id),
    actor_kind      TEXT NOT NULL,
    action          TEXT NOT NULL,
    target_type     TEXT,
    target_id       UUID,
    summary         JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip              TEXT,
    user_agent      TEXT,
    request_id      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_logs_actor_kind_known CHECK (
        actor_kind IN ('user', 'staff', 'system', 'anonymous')
    )
);

CREATE INDEX audit_logs_actor_user_id_idx ON audit_logs (actor_user_id);
CREATE INDEX audit_logs_action_idx ON audit_logs (action);
CREATE INDEX audit_logs_target_idx ON audit_logs (target_type, target_id);
CREATE INDEX audit_logs_created_at_idx ON audit_logs (created_at DESC);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_logs_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only'
        USING ERRCODE = 'integrity_constraint_violation';
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_logs_no_update
    BEFORE UPDATE ON audit_logs
    FOR EACH ROW
    EXECUTE PROCEDURE audit_logs_append_only();

CREATE TRIGGER audit_logs_no_delete
    BEFORE DELETE ON audit_logs
    FOR EACH ROW
    EXECUTE PROCEDURE audit_logs_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS audit_logs_no_delete ON audit_logs;
DROP TRIGGER IF EXISTS audit_logs_no_update ON audit_logs;

-- +goose StatementBegin
DROP FUNCTION IF EXISTS audit_logs_append_only();
-- +goose StatementEnd

DROP INDEX IF EXISTS audit_logs_created_at_idx;
DROP INDEX IF EXISTS audit_logs_target_idx;
DROP INDEX IF EXISTS audit_logs_action_idx;
DROP INDEX IF EXISTS audit_logs_actor_user_id_idx;
DROP TABLE IF EXISTS audit_logs;
