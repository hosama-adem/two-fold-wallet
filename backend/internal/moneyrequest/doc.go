// Package moneyrequest lets a user ask another user for money (FR-REQ).
//
// The request is a small state machine: pending → paid | declined | cancelled |
// expired. Every write that leaves pending is an UPDATE ... WHERE status =
// 'pending' (and still unexpired), so two concurrent pays cannot both win.
// Paying reuses transfer.TransferInTx inside the same database transaction as
// the claim, and paid_transaction_id is UNIQUE so a request links to at most
// one transfer. Expiry runs on read/pay and via the River request.expiry job.
package moneyrequest
