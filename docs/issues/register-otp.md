# feat: sign up with phone number and OTP verification

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `security` · Milestone **M1 Foundations** |
| **Branch** | `feat/register-otp` |
| **Depends on** | `feat: problem+json errors, request IDs, and HTTP middleware` (merged) |

---

## GOAL

`POST /v1/auth/register` and `POST /v1/auth/verify-phone` (FR-ID-1..3).

## WHAT YOU'LL LEARN

- Password hashing with argon2id and choosing its parameters
- OTP design: hash the code, limit attempts, expire fast, single use
- Avoiding account enumeration in responses
- Interfaces for side effects (`SMSSender`) so tests stay fast

## TASKS

- [ ] Validate phone (E.164, Ethiopian `+2519…` / `+2517…`), password policy, display name
- [ ] Store argon2id hash; create user in `pending_verification`
- [ ] Generate 6-digit OTP with `crypto/rand`, store SHA-256 hash, 5-minute expiry, max 5 attempts
- [ ] `SMSSender` interface; dev implementation logs the code
- [ ] Verify endpoint marks phone verified (wallet creation joins later)
- [ ] Resend with cooldown (60 s)

## DONE WHEN

- [ ] Tests: happy path, wrong code, expired code, too many attempts, reused code
- [ ] Register with an existing phone returns the same response shape as a new one

## OUT OF SCOPE

- Login / JWT refresh
- Transaction PIN
- Wallet creation
