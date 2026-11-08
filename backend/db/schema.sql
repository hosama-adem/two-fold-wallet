-- Canonical schema snapshot for sqlc (keep in sync with goose migrations).
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                  UUID PRIMARY KEY,
    phone_e164          CITEXT NOT NULL,
    handle              CITEXT,
    display_name        TEXT NOT NULL,
    password_hash       TEXT NOT NULL,
    pin_hash            TEXT,
    pin_failed_count    INTEGER NOT NULL DEFAULT 0,
    pin_locked_until    TIMESTAMPTZ,
    kyc_tier            SMALLINT NOT NULL DEFAULT 0,
    phone_verified_at   TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'pending_verification',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT users_phone_e164_unique UNIQUE (phone_e164),
    CONSTRAINT users_handle_unique UNIQUE (handle),
    CONSTRAINT users_phone_e164_format CHECK (phone_e164::text ~ '^\+251[79][0-9]{8}$'),
    CONSTRAINT users_kyc_tier_range CHECK (kyc_tier BETWEEN 0 AND 2),
    CONSTRAINT users_pin_failed_count_nonneg CHECK (pin_failed_count >= 0),
    CONSTRAINT users_status_known CHECK (
        status IN ('pending_verification', 'active', 'suspended', 'closed')
    )
);

CREATE TABLE otp_codes (
    id           UUID PRIMARY KEY,
    phone_e164   CITEXT NOT NULL,
    purpose      TEXT NOT NULL,
    code_hash    TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    expires_at   TIMESTAMPTZ NOT NULL,
    consumed_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT otp_codes_attempts_nonneg CHECK (attempts >= 0),
    CONSTRAINT otp_codes_purpose_known CHECK (
        purpose IN ('verify_phone', 'reset_password', 'reset_pin')
    )
);

CREATE INDEX otp_codes_active_phone_purpose_idx
    ON otp_codes (phone_e164, purpose)
    WHERE consumed_at IS NULL;

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

-- Append-only. Row triggers miss TRUNCATE, so that statement has its own.
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

CREATE TRIGGER audit_logs_no_update
    BEFORE UPDATE ON audit_logs
    FOR EACH ROW
    EXECUTE PROCEDURE audit_logs_append_only();

CREATE TRIGGER audit_logs_no_delete
    BEFORE DELETE ON audit_logs
    FOR EACH ROW
    EXECUTE PROCEDURE audit_logs_append_only();

CREATE TRIGGER audit_logs_no_truncate
    BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT
    EXECUTE PROCEDURE audit_logs_append_only();
