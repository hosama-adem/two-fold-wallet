package httpx

import (
	"log/slog"

	"github.com/gin-gonic/gin"
)

const (
	ctxKeyRequestID = "httpx_request_id"
	ctxKeyLogger    = "httpx_logger"
)

// RequestIDFromContext returns the request ID set by middleware.
func RequestIDFromContext(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// LoggerFromContext returns the request-scoped logger, if any.
func LoggerFromContext(c *gin.Context) *slog.Logger {
	if v, ok := c.Get(ctxKeyLogger); ok {
		if log, ok := v.(*slog.Logger); ok {
			return log
		}
	}
	return nil
}

func setRequestID(c *gin.Context, id string) {
	c.Set(ctxKeyRequestID, id)
}

func setLogger(c *gin.Context, log *slog.Logger) {
	c.Set(ctxKeyLogger, log)
}
