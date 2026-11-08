// Package audit appends an immutable audit log of sensitive actions.
//
// Postgres rejects UPDATE, DELETE, and TRUNCATE on audit_logs. A row records
// who acted, what they did, the target, and the request id. Summaries are
// outcomes. Record refuses password, OTP, refresh token, and PIN fields
// before the insert, and the error names the field, never the value.
package audit
