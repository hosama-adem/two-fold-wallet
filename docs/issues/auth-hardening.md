# feat: auth hardening — rate limits, sessions, and the audit log

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `security` · Milestone **M1 Foundations** |
| **Branch** | `feat/auth-hardening` |
| **Depends on** | `feat: login with short-lived access tokens and rotating refresh tokens` (merged) |

---

## GOAL

Make auth abuse-resistant and record security events: rate limits, session management APIs, and an append-only audit log.

## TASKS

- [x] Rate limits on register / verify-phone / resend-otp / login (in-memory token bucket; IP + phone)
- [x] `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}`
- [x] `audit_logs` + append-only trigger
- [x] `audit.Record` for login success/failure, session revoke, logout
- [x] Journal + README auth section

## DONE WHEN

- [x] 6th login in a minute → `429` + `Retry-After`
- [x] UPDATE/DELETE on `audit_logs` fails at the DB
