// Package provider is the payment-rail port (PRD §11.4, Day 22).
//
// Core money code depends on Provider — never on Telebirr/Chapa SDKs. The
// local simulator implements the same HTTP shapes a real rail will, and signs
// webhooks with HMAC-SHA256 so Twofold can reject tampering and replay.
//
// Amount-suffix scenarios (last two digits of amount_minor) drive the
// simulator's failure modes without config files:
//
//	00  succeed (default for any other suffix too, except the reserved ones)
//	13  decline
//	77  delay, then succeed
//	88  succeed with a duplicate webhook
//	99  accept the initiation but never webhook (stuck pending)
package provider
