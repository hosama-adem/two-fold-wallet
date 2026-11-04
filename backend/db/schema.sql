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
