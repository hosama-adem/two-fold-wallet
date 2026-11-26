// Package funding owns top-up and withdrawal lifecycles.
//
// Day 23: top-ups stay pending until a signed collection webhook credits the
// wallet (clearing → wallet). Duplicates are no-ops; late success after failure
// parks money in suspense.
//
// Day 24: saved payout destinations store AES-GCM ciphertext. Withdrawals
// reserve funds immediately (wallet → payouts_pending), then a payout webhook
// either settles (payouts_pending → clearing) or refunds via ledger.Reverse.
// Outbox topics fire in the same transaction as settle/refund/credit
// (Day 25); Day 26 turns those into notifications.
package funding
