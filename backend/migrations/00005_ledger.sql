-- +goose Up
-- Ledger bedrock (PRD §7 / §14): accounts, journals, postings, transactions.
-- Postgres enforces positive amounts, valid direction, and posting↔account currency match.
-- Balanced-journal trigger and immutability arrive in later migrations.

CREATE TABLE accounts (
    id                UUID PRIMARY KEY,
    code              TEXT NOT NULL,
    type              TEXT NOT NULL,
    normal_balance    TEXT NOT NULL,
    currency          TEXT NOT NULL,
    allow_negative    BOOLEAN NOT NULL DEFAULT false,
    balance_minor     BIGINT NOT NULL DEFAULT 0,
    version           BIGINT NOT NULL DEFAULT 0,
    owner_user_id     UUID REFERENCES users (id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT accounts_code_unique UNIQUE (code),
    CONSTRAINT accounts_id_currency_unique UNIQUE (id, currency),
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
    )
);

CREATE INDEX accounts_owner_user_id_idx ON accounts (owner_user_id);
CREATE INDEX accounts_type_idx ON accounts (type);

CREATE TABLE transactions (
    id                  UUID PRIMARY KEY,
    reference           TEXT NOT NULL,
    kind                TEXT NOT NULL,
    status              TEXT NOT NULL,
    initiator_user_id   UUID REFERENCES users (id),
    -- Wallet FKs land with the wallets table (columns exist so money APIs can wire early).
    from_wallet_id      UUID,
    to_wallet_id        UUID,
    amount_minor        BIGINT NOT NULL,
    fee_minor           BIGINT NOT NULL DEFAULT 0,
    currency            TEXT NOT NULL,
    note                TEXT,
    idempotency_key     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ,

    CONSTRAINT transactions_reference_unique UNIQUE (reference),
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
    )
);

CREATE INDEX transactions_initiator_user_id_idx ON transactions (initiator_user_id);
CREATE INDEX transactions_status_idx ON transactions (status);
CREATE INDEX transactions_created_at_idx ON transactions (created_at DESC);

CREATE TABLE journal_entries (
    id                    UUID PRIMARY KEY,
    transaction_id        UUID REFERENCES transactions (id),
    kind                  TEXT NOT NULL,
    reverses_journal_id   UUID REFERENCES journal_entries (id),
    description           TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

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
    )
);

CREATE INDEX journal_entries_transaction_id_idx ON journal_entries (transaction_id);

CREATE TABLE postings (
    id                    UUID PRIMARY KEY,
    journal_id            UUID NOT NULL REFERENCES journal_entries (id),
    account_id            UUID NOT NULL,
    direction             TEXT NOT NULL,
    amount_minor          BIGINT NOT NULL,
    currency              TEXT NOT NULL,
    balance_after_minor   BIGINT NOT NULL,
    account_version       BIGINT NOT NULL,

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
CREATE INDEX postings_account_id_idx ON postings (account_id);

CREATE TABLE transaction_events (
    id                UUID PRIMARY KEY,
    transaction_id    UUID NOT NULL REFERENCES transactions (id),
    from_status       TEXT NOT NULL,
    to_status         TEXT NOT NULL,
    reason            TEXT,
    actor             TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transaction_events_from_status_known CHECK (
        from_status IN (
            'pending',
            'processing',
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
            'completed',
            'failed',
            'reversed',
            'expired',
            'cancelled'
        )
    ),
    CONSTRAINT transaction_events_actor_known CHECK (
        actor IN ('user', 'staff', 'system', 'provider')
    )
);

CREATE INDEX transaction_events_transaction_id_idx ON transaction_events (transaction_id);
CREATE INDEX transaction_events_created_at_idx ON transaction_events (created_at DESC);

-- System accounts: fixed IDs so seeds are stable across environments.
-- Assets (debit-normal): money sitting at providers.
-- Liabilities (credit-normal): money Twofold owes (payouts in flight).
-- Revenue / equity (credit-normal): fees earned, unexplained provider money.
INSERT INTO accounts (
    id, code, type, normal_balance, currency, allow_negative, balance_minor, version, owner_user_id
) VALUES
    (
        'a0000000-0000-4000-8000-000000000001',
        'provider_clearing:telebirr',
        'asset',
        'debit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000002',
        'provider_clearing:chapa',
        'asset',
        'debit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000003',
        'payouts_pending',
        'liability',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000004',
        'fee_revenue',
        'revenue',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    ),
    (
        'a0000000-0000-4000-8000-000000000005',
        'suspense',
        'equity',
        'credit',
        'ETB',
        true,
        0,
        0,
        NULL
    );

-- +goose Down
DROP INDEX IF EXISTS transaction_events_created_at_idx;
DROP INDEX IF EXISTS transaction_events_transaction_id_idx;
DROP TABLE IF EXISTS transaction_events;

DROP INDEX IF EXISTS postings_account_id_idx;
DROP INDEX IF EXISTS postings_journal_id_idx;
DROP TABLE IF EXISTS postings;

DROP INDEX IF EXISTS journal_entries_transaction_id_idx;
DROP TABLE IF EXISTS journal_entries;

DROP INDEX IF EXISTS transactions_created_at_idx;
DROP INDEX IF EXISTS transactions_status_idx;
DROP INDEX IF EXISTS transactions_initiator_user_id_idx;
DROP TABLE IF EXISTS transactions;

DROP INDEX IF EXISTS accounts_type_idx;
DROP INDEX IF EXISTS accounts_owner_user_id_idx;
DROP TABLE IF EXISTS accounts;
