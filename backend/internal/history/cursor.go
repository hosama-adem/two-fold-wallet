package history

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// cursor is the keyset position: the (created_at, id) of the last row a page
// returned. The next page walks rows strictly older than this.
type cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// encodeCursor renders a cursor as opaque base64url. The payload is
// "<RFC3339Nano UTC>|<uuid>"; callers must treat it as opaque and round-trip it
// verbatim.
func encodeCursor(c cursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses an opaque cursor. A malformed value is a client error, not
// a 500 — the handler surfaces it as 422 validation_failed.
func decodeCursor(s string) (cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, httpx.ValidationFailed("Invalid cursor.")
	}
	ts, idStr, ok := strings.Cut(string(b), "|")
	if !ok {
		return cursor{}, httpx.ValidationFailed("Invalid cursor.")
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return cursor{}, httpx.ValidationFailed("Invalid cursor.")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return cursor{}, httpx.ValidationFailed("Invalid cursor.")
	}
	return cursor{CreatedAt: at.UTC(), ID: id}, nil
}
