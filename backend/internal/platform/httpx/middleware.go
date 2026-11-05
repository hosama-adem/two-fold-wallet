package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hosama-adem/twofold/backend/internal/platform/ids"
)

const (
	headerRequestID = "X-Request-ID"

	// DefaultBodyLimit is 1 MiB.
	DefaultBodyLimit int64 = 1 << 20
	// DefaultTimeout is the per-request handler deadline.
	DefaultTimeout = 10 * time.Second
)

// MiddlewareOption configures the standard middleware stack.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	bodyLimit int64
	timeout   time.Duration
	log       *slog.Logger
}

// WithBodyLimit overrides the max request body size.
func WithBodyLimit(n int64) MiddlewareOption {
	return func(c *middlewareConfig) { c.bodyLimit = n }
}

// WithTimeout overrides the request timeout.
func WithTimeout(d time.Duration) MiddlewareOption {
	return func(c *middlewareConfig) { c.timeout = d }
}

// WithLogger sets the base logger used by access logs and recover.
func WithLogger(log *slog.Logger) MiddlewareOption {
	return func(c *middlewareConfig) { c.log = log }
}

// Standard returns middleware in the required order:
// request ID → recover → access log → body limit → timeout.
func Standard(opts ...MiddlewareOption) []gin.HandlerFunc {
	cfg := middlewareConfig{
		bodyLimit: DefaultBodyLimit,
		timeout:   DefaultTimeout,
		log:       slog.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return []gin.HandlerFunc{
		RequestID(),
		Recover(cfg.log),
		AccessLog(cfg.log),
		MaxBodyBytes(cfg.bodyLimit),
		Timeout(cfg.timeout),
	}
}

// RequestID accepts X-Request-ID or generates one, echoes it, and stores it on the context.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" {
			var err error
			id, err = ids.New()
			if err != nil {
				id = "00000000000000000000000000000000"
			}
		}
		setRequestID(c, id)
		c.Writer.Header().Set(headerRequestID, id)
		c.Next()
	}
}

// Recover turns panics into problem+json without leaking a stack trace to the client.
func Recover(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				reqID := RequestIDFromContext(c)
				log.Error("panic recovered",
					"panic", rec,
					"request_id", reqID,
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
				)
				WriteError(c, Internal("An unexpected error occurred."))
			}
		}()
		c.Next()
	}
}

// AccessLog logs method, route template, status, duration, and request ID — never bodies.
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		start := time.Now()
		reqID := RequestIDFromContext(c)
		reqLog := log.With("request_id", reqID)
		setLogger(c, reqLog)

		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		reqLog.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// MaxBodyBytes rejects bodies larger than limit with 413 problem+json.
func MaxBodyBytes(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			WriteError(c, PayloadTooLarge("Request body must be at most 1 MB."))
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// Timeout cancels the request context after d.
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
