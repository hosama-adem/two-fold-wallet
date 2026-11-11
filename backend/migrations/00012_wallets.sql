-- +goose Up
-- A wallet is the user-facing face of one liability account.
-- status says whether the customer may move money.
-- freeze_mode says which direction is blocked:
--   none  — send and receive
--   debit — cannot send or cash out; credits (receives) still post
--   full  — no movement
-- Enforce freeze_mode in the transaction that posts, not on GET.
-- available balance is not a column. wallet_balances subtracts pending
-- withdrawal holds from the ledger cache, so the two cannot drift.

CREATE TABLE wallets (
    id             UUID PRIMARY KEY,
    user_id        UUID NOT NULL REFERENCES users (id),
    account_id     UUID NOT NULL REFERENCES accounts (id),
    currency       TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active',
    freeze_mode    TEXT NOT NULL DEFAULT 'none',
    frozen_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallets_user_currency_unique UNIQUE (user_id, currency),
    CONSTRAINT wallets_account_id_unique UNIQUE (account_id),
    CONSTRAINT wallets_id_currency_unique UNIQUE (id, currency),
    CONSTRAINT wallets_currency_etb CHECK (currency = 'ETB'),
    CONSTRAINT wallets_status_known CHECK (status IN ('active', 'frozen', 'closed')),
    CONSTRAINT wallets_freeze_mode_known CHECK (freeze_mode IN ('none', 'debit', 'full')),
    CONSTRAINT wallets_freeze_consistent CHECK (
        (
            status = 'frozen'
            AND freeze_mode IN ('debit', 'full')
            AND frozen_reason IS NOT NULL
            AND length(btrim(frozen_reason)) BETWEEN 1 AND 200
        )
        OR (
            status IN ('active', 'closed')
            AND freeze_mode = 'none'
            AND frozen_reason IS NULL
        )
    )
);

CREATE INDEX wallets_user_id_idx ON wallets (user_id);

-- A wallet must sit on the owner's ETB liability account.
CREATE OR REPLACE FUNCTION wallets_require_liability_account()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    acct accounts%ROWTYPE;
BEGIN
    SELECT * INTO acct FROM accounts WHERE id = NEW.account_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'wallets_account_required'
            USING ERRCODE = '23503';
    END IF;
    IF acct.type <> 'liability'
       OR acct.normal_balance <> 'credit'
       OR acct.currency <> NEW.currency
       OR acct.owner_user_id IS DISTINCT FROM NEW.user_id
       OR acct.allow_negative THEN
        RAISE EXCEPTION 'wallets_account_must_be_user_liability'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER wallets_require_liability_account
    BEFORE INSERT OR UPDATE OF account_id, user_id, currency ON wallets
    FOR EACH ROW
    EXECUTE PROCEDURE wallets_require_liability_account();

-- Pending withdrawal reservations. Week 4 inserts these when cash-out starts.
-- Until then the table is empty and available equals the ledger balance.
CREATE TABLE wallet_holds (
    id            UUID PRIMARY KEY,
    wallet_id     UUID NOT NULL,
    amount_minor  BIGINT NOT NULL,
    currency      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at   TIMESTAMPTZ,

    CONSTRAINT wallet_holds_amount_positive CHECK (amount_minor > 0),
    CONSTRAINT wallet_holds_kind_known CHECK (kind IN ('withdrawal')),
    CONSTRAINT wallet_holds_status_known CHECK (status IN ('pending', 'posted', 'released')),
    CONSTRAINT wallet_holds_released_at_consistent CHECK (
        (status = 'pending' AND released_at IS NULL)
        OR (status IN ('posted', 'released') AND released_at IS NOT NULL)
    ),
    CONSTRAINT wallet_holds_wallet_currency_fk
        FOREIGN KEY (wallet_id, currency) REFERENCES wallets (id, currency)
);

CREATE INDEX wallet_holds_pending_idx
    ON wallet_holds (wallet_id)
    WHERE status = 'pending';

CREATE VIEW wallet_balances AS
SELECT
    w.id AS wallet_id,
    w.user_id,
    w.account_id,
    w.currency,
    w.status,
    w.freeze_mode,
    w.frozen_reason,
    a.balance_minor AS ledger_balance_minor,
    COALESCE(h.held_minor, 0)::bigint AS held_minor,
    (a.balance_minor - COALESCE(h.held_minor, 0))::bigint AS available_minor
FROM wallets w
JOIN accounts a ON a.id = w.account_id
LEFT JOIN (
    SELECT wallet_id, SUM(amount_minor)::bigint AS held_minor
    FROM wallet_holds
    WHERE status = 'pending'
    GROUP BY wallet_id
) h ON h.wallet_id = w.id;

-- +goose Down
DROP VIEW IF EXISTS wallet_balances;
DROP TABLE IF EXISTS wallet_holds;
DROP TRIGGER IF EXISTS wallets_require_liability_account ON wallets;
DROP FUNCTION IF EXISTS wallets_require_liability_account();
DROP TABLE IF EXISTS wallets;

-- Wallet rows are gone. Drop the liability accounts that never received a
-- posting. Accounts with history stay: postings are append-only.
DELETE FROM accounts
WHERE code LIKE 'wallet:%'
  AND owner_user_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM postings p WHERE p.account_id = accounts.id
  );
