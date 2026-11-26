// Package outbox is the transactional outbox (PRD §11.1, ADR 0004).
//
// Money paths call Write inside the same database transaction as the journal.
// A dispatcher claims undispatched rows with FOR UPDATE SKIP LOCKED and
// publishes them to River. Crash after commit and before dispatch leaves the
// row durable — the next tick delivers it.
package outbox
