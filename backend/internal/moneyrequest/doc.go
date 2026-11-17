// Package moneyrequest lets a user ask another user for money (FR-REQ).
//
// The request is a small state machine: pending → paid | declined | cancelled |
// expired. Every write that leaves pending is an UPDATE ... WHERE status =
// 'pending' (and still unexpired), so two concurrent pays cannot both win.
// Paying reuses transfer.TransferInTx inside the same database transaction as
// the claim, and paid_transaction_id is UNIQUE so a request links to at most
// one transfer. Expiry is applied on read and on pay for now; a River job lands
// on Day 25.
package moneyrequest
