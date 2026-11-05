# feat: login with short-lived access tokens and rotating refresh tokens

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `security` · Milestone **M1 Foundations** |
| **Branch** | `feat/login-refresh` (or `feat/backend/login-short`) |
| **Depends on** | `feat: sign up with phone number and OTP verification` (merged) |

---

## GOAL

`POST /v1/auth/login`, `POST /v1/auth/refresh`, `POST /v1/auth/logout`, and `GET /v1/me` (FR-ID-4..6).

## WHAT YOU'LL LEARN

- Access vs refresh tokens, and why access tokens live 15 minutes
- Refresh token rotation and reuse detection (token families)
- Ed25519 (EdDSA) JWT signing and the `kid` header for key rotation
- Constant-time comparison and timing attacks on login

## TASKS

- [x] `sessions` migration (`id`, `user_id`, `family_id`, `refresh_hash`, UA/IP, revoke/replace timestamps)
- [x] JWT access token (15 min) with `sub`, `sid`, `kid`; EdDSA keys from env; `aud=twofold-app`
- [x] Opaque refresh token (30 days); store only its hash on the session
- [x] Refresh: rotate, mark old as replaced; reuse of an old token revokes the whole family
- [x] Auth middleware puts `UserID` and `SessionID` in context
- [x] `GET /v1/me` returns profile and tier

## DONE WHEN

- [x] Test: reusing a rotated refresh token revokes every session in the family
- [x] Test: expired and tampered tokens return 401 problem+json
- [x] Login with wrong password does not leak whether the phone exists (same shape / timing care)

## OUT OF SCOPE

- Rate limits / `Retry-After`
- Session list / revoke APIs (`GET/DELETE /v1/me/sessions`)
- Audit log
- Transaction PIN
