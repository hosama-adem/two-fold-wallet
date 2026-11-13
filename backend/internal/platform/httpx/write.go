package httpx

import (
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
)

// WriteError maps err to an RFC 9457 problem+json response.
// It never writes SQL strings or stack traces to the client.
func WriteError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	reqID := RequestIDFromContext(c)
	he := MapCommon(err)

	detail := he.Detail
	if he.Status >= 500 {
		detail = "An unexpected error occurred."
		log := LoggerFromContext(c)
		if log != nil {
			log.Error("request failed",
				"error", err.Error(),
				"request_id", reqID,
				"path", c.FullPath(),
				"method", c.Request.Method,
			)
		} else {
			slog.Error("request failed", "error", err.Error(), "request_id", reqID)
		}
	}

	if he.RetryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.Itoa(he.RetryAfterSeconds))
	}

	p := newProblem(he.Status, he.Code, he.Title, detail, reqID)
	p.TriesRemaining = he.TriesRemaining
	p.LockedUntil = he.LockedUntil
	p.Limit = he.Limit
	p.RemainingMinor = he.RemainingMinor
	WriteProblem(c, p)
}

// WriteProblem writes an already-built problem document.
func WriteProblem(c *gin.Context, p Problem) {
	if p.RequestID == "" {
		p.RequestID = RequestIDFromContext(c)
	}
	if c.Writer.Written() {
		return
	}

	c.Header("X-Request-ID", p.RequestID)
	c.Header("Content-Type", contentType)
	c.Abort()
	c.Status(p.Status)

	enc := json.NewEncoder(c.Writer)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(p)
}
