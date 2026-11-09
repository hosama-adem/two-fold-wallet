-- +goose Up
-- A wallet is one ETB liability account plus the status a customer sees.
-- Phone verification inserts both rows in one transaction. The unique
-- (user_id, currency) constraint is what makes a second insert lose.
--
-- available = ledger − pending cash-outs. Ledger balance stays on accounts,
-- where it can be rebuilt from postings. Pending cash-outs are transactions
-- that have not posted yet (Week 4). A cached available column would drift
-- from those rows, so the number is a view.
--
-- Freeze is data here. Send / receive / cash-out checks belong in the money
-- transaction (Day 15 transfers, Week 4 cash-out), which calls wallet.Gate.
-- outbound: cannot send or cash out, can still receive.
-- full: no movement either way.
-- closed: no movement, and the ledger balance must already be zero.

CREATE TABLE wallets (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users (id),
    account_id      UUID NOT NULL REFERENCES accounts (id),
    currency        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    freeze_mode     TEXT NOT NULL DEFAULT 'none',
    frozen_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallets_user_currency_unique UNIQUE (user_id, currency),
    CONSTRAINT wallets_account_unique UNIQUE (account_id),
    CONSTRAINT wallets_currency_etb CHECK (currency = 'ETB'),
    CONSTRAINT wallets_status_known CHECK (
        status IN ('active', 'frozen', 'closed')
    ),
    CONSTRAINT wallets_freeze_mode_known CHECK (
        freeze_mode IN ('none', 'outbound', 'full')
    ),
    CONSTRAINT wallets_frozen_reason_len CHECK (
        frozen_reason IS NULL OR (
            length(btrim(frozen_reason)) > 0
            AND length(frozen_reason) <= 500
        )
    ),
    CONSTRAINT wallets_freeze_consistent CHECK (
        (
            status = 'frozen'
            AND freeze_mode IN ('outbound', 'full')
            AND frozen_reason IS NOT NULL
        )
        OR (
            status IN ('active', 'closed')
            AND freeze_mode = 'none'
            AND frozen_reason IS NULL
        )
    ),
    CONSTRAINT wallets_account_currency_fk
        FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency)
);

-- Columns existed before this migration, with no table to point at.
-- Drop ids that do not match a wallet so the foreign key can be added
-- on a database that already has transaction rows.
UPDATE transactions AS t
SET from_wallet_id = NULL
WHERE from_wallet_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM wallets AS w WHERE w.id = t.from_wallet_id);

UPDATE transactions AS t
SET to_wallet_id = NULL
WHERE to_wallet_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM wallets AS w WHERE w.id = t.to_wallet_id);

ALTER TABLE transactions
    ADD CONSTRAINT transactions_from_wallet_fk
        FOREIGN KEY (from_wallet_id) REFERENCES wallets (id);

ALTER TABLE transactions
    ADD CONSTRAINT transactions_to_wallet_fk
        FOREIGN KEY (to_wallet_id) REFERENCES wallets (id);

CREATE INDEX transactions_pending_cash_out_idx
    ON transactions (from_wallet_id)
    WHERE kind = 'cash_out'
      AND status IN ('pending', 'processing', 'needs_review');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION wallets_guard_account()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    acct accounts%ROWTYPE;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.user_id <> OLD.user_id
            OR NEW.account_id <> OLD.account_id
            OR NEW.currency <> OLD.currency
        THEN
            RAISE EXCEPTION 'wallet owner, account, and currency are immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    SELECT * INTO acct FROM accounts WHERE id = NEW.account_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'wallet account missing'
            USING ERRCODE = '23514';
    END IF;
    IF acct.type <> 'liability'
        OR acct.normal_balance <> 'credit'
        OR acct.allow_negative
        OR acct.owner_user_id IS DISTINCT FROM NEW.user_id
        OR acct.currency <> NEW.currency
    THEN
        RAISE EXCEPTION 'wallet account must be the owner''s non-negative ETB liability'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER wallets_guard_account_trg
    BEFORE INSERT OR UPDATE ON wallets
    FOR EACH ROW
    EXECUTE PROCEDURE wallets_guard_account();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION wallets_closed_zero_balance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'closed' AND EXISTS (
        SELECT 1
        FROM accounts a
        WHERE a.id = NEW.account_id
          AND a.balance_minor <> 0
    ) THEN
        RAISE EXCEPTION 'wallet cannot close with a non-zero ledger balance'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER wallets_closed_zero_balance_trg
    BEFORE INSERT OR UPDATE ON wallets
    FOR EACH ROW
    EXECUTE PROCEDURE wallets_closed_zero_balance();

CREATE VIEW wallet_balances AS
SELECT
    w.id,
    w.user_id,
    w.account_id,
    w.currency,
    w.status,
    w.freeze_mode,
    a.balance_minor AS ledger_balance_minor,
    COALESCE(h.pending_minor, 0)::bigint AS pending_withdrawal_minor,
    (a.balance_minor - COALESCE(h.pending_minor, 0))::bigint AS available_minor
FROM wallets w
JOIN accounts a ON a.id = w.account_id
LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(t.amount_minor + t.fee_minor), 0)::bigint AS pending_minor
    FROM transactions t
    WHERE t.from_wallet_id = w.id
      AND t.kind = 'cash_out'
      AND t.status IN ('pending', 'processing', 'needs_review')
) h ON true;

-- +goose Down
DROP VIEW IF EXISTS wallet_balances;
DROP TRIGGER IF EXISTS wallets_closed_zero_balance_trg ON wallets;
DROP TRIGGER IF EXISTS wallets_guard_account_trg ON wallets;
DROP FUNCTION IF EXISTS wallets_closed_zero_balance();
DROP FUNCTION IF EXISTS wallets_guard_account();
-- The wallet rows are about to disappear. Clear the links first so a
-- later up can add the foreign keys again.
UPDATE transactions
SET from_wallet_id = NULL
WHERE from_wallet_id IS NOT NULL;
UPDATE transactions
SET to_wallet_id = NULL
WHERE to_wallet_id IS NOT NULL;
DROP INDEX IF EXISTS transactions_pending_cash_out_idx;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_to_wallet_fk;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_from_wallet_fk;
DROP TABLE IF EXISTS wallets;
