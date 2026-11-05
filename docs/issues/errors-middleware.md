# feat: problem+json errors, request IDs, and HTTP middleware

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · Milestone **M1 Foundations** |
| **Branch** | `feat/errors-middleware` |
| **Depends on** | `feat: migrations with goose, queries with sqlc, and the users table` (merged) |

---

## GOAL

Every response — success or failure — is predictable, traceable, and safe.

## WHAT YOU'LL LEARN

- RFC 9457 problem details and stable machine-readable error codes
- Separating domain errors from transport errors in Go (`errors.Is` / `errors.As`)
- Middleware order: request ID → recover → logging → timeout → auth
- Why you never leak SQL or stack traces to clients

## TASKS

- [ ] `internal/platform/httpx`: `Problem` type, `WriteError(c, err)` mapping domain errors to status + code
- [ ] Request ID middleware (accept `X-Request-ID` or generate, echo it back, put it in logs)
- [ ] Recover middleware returning a problem response, logging the panic
- [ ] Access log middleware (method, route template, status, duration, request ID) — no bodies
- [ ] Body size limit (1 MB) and request timeout (10 s)
- [ ] JSON decoding that rejects unknown fields

## DONE WHEN

- [ ] Table tests cover 400, 404, 409, 413, 422, 500 shapes
- [ ] A panic in a handler returns `problem+json` with the request ID and no stack trace

## OUT OF SCOPE

- Auth register / login endpoints
- Ledger / money APIs
