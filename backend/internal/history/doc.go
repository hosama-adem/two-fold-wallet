// Package history serves the transaction ledger a customer can see, search, and
// export (FR-HIS): a newest-first list, a per-transaction receipt, and a CSV
// stream. All three enforce the same participant check — a user sees a row only
// if they initiated it or own one of its wallets. Non-participants get 404, so
// the existence of a reference never leaks.
//
// Paging is keyset, not OFFSET. The list orders by (created_at DESC, id DESC)
// and the cursor is an opaque, base64url-encoded (created_at, id) pair taken
// from the last row returned. The next page asks for rows strictly older than
// that pair with a single row-comparison, so pages stay O(limit) no matter how
// deep the history is, and newer rows arriving mid-pagination never shift the
// window to cause duplicates or gaps. OFFSET would re-scan and skip every time a
// row is inserted ahead of the current page — the exact bug keyset avoids.
package history
