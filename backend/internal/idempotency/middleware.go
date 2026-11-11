package idempotency

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// CurrentUser reads the authenticated user id set by access-token middleware.
type CurrentUser func(c *gin.Context) (uuid.UUID, bool)

// Middleware enforces Idempotency-Key on routes that opt in.
// Mount it after authentication. It claims the key, runs the handler once,
// stores the response for replay, and releases the claim on 5xx so retries
// can proceed.
func Middleware(store *Store, currentUser CurrentUser) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil || currentUser == nil {
			httpx.WriteError(c, httpx.Internal("Idempotency middleware is misconfigured."))
			return
		}

		userID, ok := currentUser(c)
		if !ok || userID == uuid.Nil {
			httpx.WriteError(c, errUnauthorized())
			return
		}

		rawKey := strings.TrimSpace(c.GetHeader(headerIdempotency))
		if rawKey == "" {
			httpx.WriteError(c, errMissingKey())
			return
		}
		key, err := parseKey(rawKey)
		if err != nil {
			httpx.WriteError(c, errInvalidKey())
			return
		}

		body, err := readBody(c)
		if err != nil {
			httpx.WriteError(c, err)
			return
		}

		hash, err := RequestHash(c.Request.Method, c.Request.URL.Path, body)
		if err != nil {
			httpx.WriteError(c, errInvalidBody())
			return
		}

		claim, err := store.Claim(c.Request.Context(), userID, key, hash)
		if err != nil {
			httpx.WriteError(c, httpx.Internal("Could not claim idempotency key."))
			return
		}

		if !claim.Won {
			handleExisting(c, claim.Existing, hash)
			return
		}

		cap := newCapturingWriter(c.Writer)
		c.Writer = cap

		defer func() {
			if rec := recover(); rec != nil {
				_ = store.Release(c.Request.Context(), userID, key)
				panic(rec)
			}
		}()

		c.Next()

		status := cap.ResponseWriter.Status()
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 500 {
			_ = store.Release(c.Request.Context(), userID, key)
			return
		}

		ct := cap.Header().Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		if completeErr := store.Complete(c.Request.Context(), userID, key, StoredResponse{
			StatusCode:  status,
			Body:        cap.Bytes(),
			ContentType: ct,
		}); completeErr != nil {
			log := httpx.LoggerFromContext(c)
			if log != nil {
				log.Error("idempotency complete failed",
					"error", completeErr.Error(),
					"user_id", userID.String(),
					"key", key,
				)
			}
		}
	}
}

func handleExisting(c *gin.Context, existing *Record, hash string) {
	if existing == nil {
		httpx.WriteError(c, httpx.Internal("Idempotency state missing."))
		return
	}
	if existing.RequestHash != hash {
		httpx.WriteError(c, errKeyReused())
		return
	}
	if existing.State == stateInProgress {
		httpx.WriteError(c, errInProgress())
		return
	}
	if existing.State != stateCompleted {
		httpx.WriteError(c, httpx.Internal("Unknown idempotency state."))
		return
	}

	ct := existing.ResponseContentType
	if ct == "" {
		ct = "application/json"
	}
	c.Header(headerReplay, "true")
	c.Data(existing.ResponseCode, ct, existing.ResponseBody)
	c.Abort()
}

func parseKey(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}
	if id.Version() != 4 {
		return "", errInvalidKey()
	}
	return strings.ToLower(id.String()), nil
}

func readBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "http: request body too large") {
			return nil, httpx.PayloadTooLarge("Request body must be at most 1 MB.")
		}
		return nil, httpx.BadRequest("Could not read request body.")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}
