// Package idempotency stores Idempotency-Key lookups and safe replays (PRD §9).
//
// Contract:
//   - Same (user, key) + same request hash → replay stored status + body
//     with Idempotent-Replayed: true
//   - Same key + different body → 422 idempotency_key_reused
//   - Concurrent claim while in_progress → 409 request_in_progress
//
// Mount Middleware after auth on routes that move money or create resources.
// Transfers wire this up on Day 15; the store and middleware ship here.
package idempotency
