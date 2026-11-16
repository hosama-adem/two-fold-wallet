-- +goose Up
-- Composite indexes for the transaction history list (FR-HIS-1).
-- The list walks newest-first with a keyset cursor and filters rows to the
-- caller's three participant predicates (initiator, from_wallet, to_wallet).
-- Each index carries the keyset sort (created_at DESC, id DESC) so Postgres can
-- satisfy an access path per predicate and BitmapOr them together — no OFFSET.

CREATE INDEX transactions_from_wallet_created_id_idx
    ON transactions (from_wallet_id, created_at DESC, id DESC);

CREATE INDEX transactions_to_wallet_created_id_idx
    ON transactions (to_wallet_id, created_at DESC, id DESC);

CREATE INDEX transactions_initiator_created_id_idx
    ON transactions (initiator_user_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS transactions_initiator_created_id_idx;
DROP INDEX IF EXISTS transactions_to_wallet_created_id_idx;
DROP INDEX IF EXISTS transactions_from_wallet_created_id_idx;
