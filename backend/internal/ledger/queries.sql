-- name: InsertJournalEntry :one
INSERT INTO journal_entries (
    id, transaction_id, kind, description
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: InsertPosting :one
INSERT INTO postings (
    id, journal_id, account_id, direction, amount_minor,
    currency, balance_after_minor, account_version
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetAccountByID :one
SELECT * FROM accounts
WHERE id = $1;

-- name: GetAccountByCode :one
SELECT * FROM accounts
WHERE code = $1;

-- name: ListAccountsByOwner :many
SELECT * FROM accounts
WHERE owner_user_id = $1
ORDER BY code;

-- name: ListSystemAccounts :many
SELECT * FROM accounts
WHERE owner_user_id IS NULL
ORDER BY code;

-- name: GetJournalEntryByID :one
SELECT * FROM journal_entries
WHERE id = $1;

-- name: ListPostingsByJournalID :many
SELECT * FROM postings
WHERE journal_id = $1
ORDER BY created_at, id;

-- name: ListPostingsByAccountID :many
SELECT * FROM postings
WHERE account_id = $1
ORDER BY created_at, id;

-- name: GetTransactionByID :one
SELECT * FROM transactions
WHERE id = $1;

-- name: GetTransactionByReference :one
SELECT * FROM transactions
WHERE reference = $1;

-- name: ListTransactionEventsByTransactionID :many
SELECT * FROM transaction_events
WHERE transaction_id = $1
ORDER BY created_at, id;
