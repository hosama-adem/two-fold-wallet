// Package funding owns top-up (and later withdrawal) lifecycles.
//
// Day 23: POST /v1/topups creates a pending top_up + provider_payments row and
// returns checkout info. Money is credited only when POST
// /webhooks/providers/{provider} verifies HMAC, dedupes on (provider, event_id),
// and posts Dr provider_clearing / Cr wallet. Late success after failure goes
// to suspense with an audit alert. PollStale is the Day-25 worker hook.
package funding
