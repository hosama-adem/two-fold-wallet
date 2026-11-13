// Package limit enforces KYC tier caps on every money movement (PRD §12.1).
//
// Caps live in tier_limits. Daily and monthly usage is summed from liability
// debit postings in Africa/Addis_Ababa calendar windows — not stored counters.
// Check must run inside the money transaction after wallet locks and before
// the journal posts, so concurrent transfers cannot slip past a cap.
package limit
