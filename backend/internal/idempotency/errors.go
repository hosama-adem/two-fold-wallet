package idempotency

import (
	"errors"
	"net/http"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	CodeKeyReused      = "idempotency_key_reused"
	CodeInProgress     = "request_in_progress"
	CodeMissingKey     = "idempotency_key_required"
	CodeInvalidKey     = "idempotency_key_invalid"
	headerReplay       = "Idempotent-Replayed"
	headerIdempotency  = "Idempotency-Key"
	inProgressRetrySec = 1
)

var (
	// ErrInvalidBody means the request body is not canonicalizable JSON.
	ErrInvalidBody = errors.New("idempotency: invalid body")
	// ErrNotClaimed means Complete/Release found no in_progress row.
	ErrNotClaimed = errors.New("idempotency: key not in progress")
)

func errMissingKey() *httpx.Error {
	return httpx.New(
		http.StatusBadRequest,
		CodeMissingKey,
		"Bad Request",
		"Idempotency-Key header is required.",
	)
}

func errInvalidKey() *httpx.Error {
	return httpx.New(
		http.StatusBadRequest,
		CodeInvalidKey,
		"Bad Request",
		"Idempotency-Key must be a UUID v4.",
	)
}

func errUnauthorized() *httpx.Error {
	return httpx.New(
		http.StatusUnauthorized,
		"unauthorized",
		"Unauthorized",
		"Authentication is required for idempotent requests.",
	)
}

func errKeyReused() *httpx.Error {
	return httpx.New(
		http.StatusUnprocessableEntity,
		CodeKeyReused,
		"Unprocessable Entity",
		"Idempotency-Key was already used with a different request body.",
	)
}

func errInProgress() *httpx.Error {
	return &httpx.Error{
		Status:            http.StatusConflict,
		Code:              CodeInProgress,
		Title:             "Conflict",
		Detail:            "A request with this Idempotency-Key is already in progress.",
		RetryAfterSeconds: inProgressRetrySec,
	}
}

func errInvalidBody() *httpx.Error {
	return httpx.BadRequest("Malformed JSON body.")
}

// TTL is how long a key is kept for replay (PRD §9.1).
const TTL = 24 * time.Hour
