-- name: ListWalletIDsForUser :many
-- The caller's wallet ids, fed to the list/export participant predicate as a
-- uuid[] so from_wallet_id/to_wallet_id compare against constants and the
-- composite indexes (migration 00016) are usable.
SELECT id FROM wallets WHERE user_id = @user_id;

-- name: ListTransactionsForUser :many
-- Newest-first keyset page of the caller's transactions (FR-HIS-1).
-- The caller sees a row if they initiated it or own either wallet. Wallet ids
-- arrive as a uuid[] (not an EXISTS join) so Postgres can BitmapOr the
-- transactions_{from,to}_wallet_created_id_idx and transactions_initiator_created_id_idx
-- indexes instead of filtering the whole table. Filters (kind/status/from/to)
-- are optional. The cursor is the (created_at, id) of the last row of the
-- previous page; the row-comparison walks strictly older rows. `from` is
-- inclusive, `to` is exclusive. Never OFFSET.
SELECT
    t.id,
    t.reference,
    t.kind,
    t.status,
    t.amount_minor,
    t.fee_minor,
    t.currency,
    t.note,
    t.from_wallet_id,
    t.to_wallet_id,
    t.created_at,
    t.completed_at,
    fw.user_id AS from_user_id,
    tw.user_id AS to_user_id,
    fu.display_name AS from_display_name,
    fu.handle AS from_handle,
    fu.phone_e164 AS from_phone,
    tu.display_name AS to_display_name,
    tu.handle AS to_handle,
    tu.phone_e164 AS to_phone
FROM transactions t
LEFT JOIN wallets fw ON fw.id = t.from_wallet_id
LEFT JOIN wallets tw ON tw.id = t.to_wallet_id
LEFT JOIN users fu ON fu.id = fw.user_id
LEFT JOIN users tu ON tu.id = tw.user_id
WHERE (
    t.initiator_user_id = @user_id
    OR t.from_wallet_id = ANY(@wallet_ids::uuid[])
    OR t.to_wallet_id = ANY(@wallet_ids::uuid[])
)
AND (sqlc.narg('kind')::text IS NULL OR t.kind = sqlc.narg('kind')::text)
AND (sqlc.narg('status')::text IS NULL OR t.status = sqlc.narg('status')::text)
AND (sqlc.narg('from_time')::timestamptz IS NULL OR t.created_at >= sqlc.narg('from_time')::timestamptz)
AND (sqlc.narg('to_time')::timestamptz IS NULL OR t.created_at < sqlc.narg('to_time')::timestamptz)
AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (t.created_at, t.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
)
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg('row_limit')::int;

-- name: GetTransactionForUser :one
-- One receipt by reference, visible only to a participant (FR-HIS-2).
-- A non-participant gets no row (handler maps that to 404, never leaking existence).
SELECT
    t.id,
    t.reference,
    t.kind,
    t.status,
    t.amount_minor,
    t.fee_minor,
    t.currency,
    t.note,
    t.from_wallet_id,
    t.to_wallet_id,
    t.created_at,
    t.completed_at,
    fw.user_id AS from_user_id,
    tw.user_id AS to_user_id,
    fu.display_name AS from_display_name,
    fu.handle AS from_handle,
    fu.phone_e164 AS from_phone,
    tu.display_name AS to_display_name,
    tu.handle AS to_handle,
    tu.phone_e164 AS to_phone
FROM transactions t
LEFT JOIN wallets fw ON fw.id = t.from_wallet_id
LEFT JOIN wallets tw ON tw.id = t.to_wallet_id
LEFT JOIN users fu ON fu.id = fw.user_id
LEFT JOIN users tu ON tu.id = tw.user_id
WHERE t.reference = @reference
AND (
    t.initiator_user_id = @user_id
    OR EXISTS (SELECT 1 FROM wallets w WHERE w.id = t.from_wallet_id AND w.user_id = @user_id)
    OR EXISTS (SELECT 1 FROM wallets w WHERE w.id = t.to_wallet_id AND w.user_id = @user_id)
);

-- name: GetReceiptBalanceAfterForUser :one
-- Running balance the caller's wallet reached on this transaction's journal
-- (FR-HIS-2). Picks the posting with the highest account_version on the
-- caller's liability account. Returns no row while the transaction is pending
-- (no journal yet); the service treats that as a null balance.
SELECT p.balance_after_minor
FROM journal_entries je
JOIN postings p ON p.journal_id = je.id
JOIN wallets w ON w.account_id = p.account_id
WHERE je.transaction_id = @transaction_id
  AND w.user_id = @user_id
ORDER BY p.account_version DESC
LIMIT 1;

-- name: ListBalancesAfterForUser :many
-- Same balance as GetReceiptBalanceAfterForUser, for a page of transactions.
-- Export calls this once per keyset batch instead of once per row. A transaction
-- with no journal (still pending) is absent; the caller treats that as null.
SELECT DISTINCT ON (je.transaction_id)
    je.transaction_id,
    p.balance_after_minor
FROM journal_entries je
JOIN postings p ON p.journal_id = je.id
JOIN wallets w ON w.account_id = p.account_id
WHERE je.transaction_id = ANY(@transaction_ids::uuid[])
  AND w.user_id = @user_id
ORDER BY je.transaction_id, p.account_version DESC;
