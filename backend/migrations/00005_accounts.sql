-- +goose Up
-- Chart of accounts (PRD §7).
-- A user wallet is a liability: the balance is money Twofold owes the customer.
-- Assets and expenses are debit-normal (debits - credits).
-- Liabilities, revenue, and equity are credit-normal (credits - debits).
-- allow_negative defaults to false so a wallet cache cannot cross zero.
-- System accounts opt in when a temporary negative is a real state.
-- Keep the system-code list aligned with 00009_seed_system_accounts.sql
-- and internal/ledger/codes.go.

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
    -- Composite target so a posting cannot name this account in another currency.
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
    -- TEXT rather than char(3): char pads with spaces and breaks equality.
    CONSTRAINT accounts_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT accounts_version_nonneg CHECK (version >= 0),
    CONSTRAINT accounts_balance_nonneg_unless_allowed CHECK (
        allow_negative OR balance_minor >= 0
    ),
    -- No owner means a platform bucket, and a platform code has no owner.
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

-- +goose Down
DROP INDEX IF EXISTS accounts_system_idx;
DROP INDEX IF EXISTS accounts_owner_user_id_idx;
DROP TABLE IF EXISTS accounts;
