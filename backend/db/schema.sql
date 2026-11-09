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

-- Ledger (migrations 00005–00009). Seeds live in 00009, not in this snapshot.
-- A user wallet is a liability. Postings store direction + a positive amount.

CREATE TABLE accounts (
    id               UUID PRIMARY KEY,
    code             TEXT NOT NULL,
    type             TEXT NOT NULL,
    normal_balance   TEXT NOT NULL,
    currency         TEXT NOT NULL,
    allow_negative   BOOLEAN NOT NULL DEFAULT false,
    balance_minor    BIGINT NOT NULL DEFAULT 0,
    version          BIGINT NOT NULL DEFAULT 0,
    owner_user_id    UUID REFERENCES users (id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT accounts_code_unique UNIQUE (code),
    CONSTRAINT accounts_id_currency_unique UNIQUE (id, currency),
    CONSTRAINT accounts_code_format CHECK (code ~ '^[a-z0-9_.:-]{1,64}$'),
    CONSTRAINT accounts_type_known CHECK (
        type IN ('asset', 'liability', 'revenue', 'expense', 'equity')
    ),
    CONSTRAINT accounts_normal_balance_known CHECK (
        normal_balance IN ('debit', 'credit')
    ),
    CONSTRAINT accounts_type_normal_balance_match CHECK (
        (type IN ('asset', 'expense') AND normal_balance = 'debit')
        OR (type IN ('liability', 'revenue', 'equity') AND normal_balance = 'credit')
    ),
    CONSTRAINT accounts_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT accounts_version_nonneg CHECK (version >= 0),
    CONSTRAINT accounts_balance_nonneg_unless_allowed CHECK (
        allow_negative OR balance_minor >= 0
    ),
    CONSTRAINT accounts_system_code_iff_ownerless CHECK (
        (owner_user_id IS NULL) = (
            code IN (
                'provider_clearing:telebirr',
                'provider_clearing:chapa',
                'payouts_pending',
                'fee_revenue',
                'suspense'
            )
        )
    )
);

CREATE INDEX accounts_owner_user_id_idx ON accounts (owner_user_id);

CREATE INDEX accounts_system_idx
    ON accounts (code)
    WHERE owner_user_id IS NULL;

CREATE TABLE transactions (
    id                 UUID PRIMARY KEY,
    reference          TEXT NOT NULL,
    kind               TEXT NOT NULL,
    status             TEXT NOT NULL,
    initiator_user_id  UUID REFERENCES users (id),
    from_wallet_id     UUID,
    to_wallet_id       UUID,
    amount_minor       BIGINT NOT NULL,
    fee_minor          BIGINT NOT NULL DEFAULT 0,
    currency           TEXT NOT NULL,
    note               TEXT,
    idempotency_key    TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ,

    CONSTRAINT transactions_reference_unique UNIQUE (reference),
    CONSTRAINT transactions_reference_present CHECK (length(btrim(reference)) > 0),
    CONSTRAINT transactions_kind_known CHECK (
        kind IN (
            'p2p',
            'top_up',
            'cash_out',
            'merchant_payment',
            'money_request',
            'adjustment'
        )
    ),
    CONSTRAINT transactions_status_known CHECK (
        status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transactions_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT transactions_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT transactions_fee_nonneg CHECK (
        fee_minor >= 0 AND fee_minor <= 1000000000000000
    ),
    CONSTRAINT transactions_wallets_distinct CHECK (
        from_wallet_id IS NULL
        OR to_wallet_id IS NULL
        OR from_wallet_id <> to_wallet_id
    ),
    CONSTRAINT transactions_idempotency_key_uuid CHECK (
        idempotency_key IS NULL
        OR idempotency_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    )
);

CREATE UNIQUE INDEX transactions_user_idempotency_key_idx
    ON transactions (initiator_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND initiator_user_id IS NOT NULL;

CREATE INDEX transactions_initiator_user_id_idx ON transactions (initiator_user_id);
CREATE INDEX transactions_status_idx ON transactions (status);
CREATE INDEX transactions_created_at_idx ON transactions (created_at DESC);

CREATE TABLE transaction_events (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL REFERENCES transactions (id),
    from_status      TEXT NOT NULL,
    to_status        TEXT NOT NULL,
    reason           TEXT,
    actor            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transaction_events_from_status_known CHECK (
        from_status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transaction_events_to_status_known CHECK (
        to_status IN (
            'pending',
            'processing',
            'needs_review',
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transaction_events_status_moves CHECK (from_status <> to_status),
    CONSTRAINT transaction_events_actor_known CHECK (
        actor IN ('user', 'staff', 'system', 'provider')
    )
);

CREATE INDEX transaction_events_transaction_id_idx
    ON transaction_events (transaction_id, created_at);

CREATE TABLE journal_entries (
    id                   UUID PRIMARY KEY,
    transaction_id       UUID REFERENCES transactions (id),
    kind                 TEXT NOT NULL,
    reverses_journal_id  UUID REFERENCES journal_entries (id),
    description          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT journal_entries_reverses_unique UNIQUE (reverses_journal_id),
    CONSTRAINT journal_entries_kind_known CHECK (
        kind IN (
            'p2p',
            'top_up',
            'cash_out',
            'merchant_payment',
            'money_request',
            'adjustment',
            'reversal'
        )
    ),
    CONSTRAINT journal_entries_reversal_link CHECK (
        (
            kind = 'reversal'
            AND reverses_journal_id IS NOT NULL
            AND reverses_journal_id <> id
        )
        OR (
            kind <> 'reversal'
            AND reverses_journal_id IS NULL
        )
    )
);

CREATE INDEX journal_entries_transaction_id_idx ON journal_entries (transaction_id);
CREATE INDEX journal_entries_created_at_idx ON journal_entries (created_at DESC);

CREATE TABLE postings (
    id                   UUID PRIMARY KEY,
    journal_id           UUID NOT NULL REFERENCES journal_entries (id),
    account_id           UUID NOT NULL,
    direction            TEXT NOT NULL,
    amount_minor         BIGINT NOT NULL,
    currency             TEXT NOT NULL,
    balance_after_minor  BIGINT NOT NULL,
    account_version      BIGINT NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT postings_direction_known CHECK (direction IN ('debit', 'credit')),
    CONSTRAINT postings_amount_positive CHECK (
        amount_minor > 0 AND amount_minor <= 1000000000000000
    ),
    CONSTRAINT postings_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT postings_account_version_nonneg CHECK (account_version >= 0),
    CONSTRAINT postings_account_currency_fk
        FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency)
);

CREATE INDEX postings_journal_id_idx ON postings (journal_id);
CREATE INDEX postings_account_created_at_idx ON postings (account_id, created_at);

CREATE OR REPLACE FUNCTION assert_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    jid uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        jid := OLD.journal_id;
    ELSE
        jid := NEW.journal_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM postings
        WHERE journal_id = jid
        GROUP BY currency
        HAVING SUM(CASE WHEN direction = 'debit' THEN amount_minor ELSE 0 END)
            <> SUM(CASE WHEN direction = 'credit' THEN amount_minor ELSE 0 END)
    ) THEN
        RAISE EXCEPTION
            'postings_journal_balanced: journal % debits do not equal credits',
            jid
            USING ERRCODE = '23514';
    END IF;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER postings_journal_balanced
    AFTER INSERT OR UPDATE OR DELETE ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE PROCEDURE assert_journal_balanced();
