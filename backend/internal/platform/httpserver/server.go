package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/history"
	"github.com/hosama-adem/twofold/backend/internal/kyc"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/platform/openapi"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// Server wraps the HTTP listener and Gin engine.
type Server struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	engine *gin.Engine
	http   *http.Server
	log    *slog.Logger
}

// Options configures optional HTTP mounts.
type Options struct {
	Auth          *auth.Handler
	Wallets       *wallet.Handler
	Transfers     *transfer.Handler
	KYC           *kyc.Handler
	History       *history.Handler
	MoneyRequests *moneyrequest.Handler
	// RequestTimeout overrides the per-request deadline. Zero keeps the
	// standard 10s timeout. The concurrency suite raises it: a thousand
	// transfers on one pair queue on the row lock, and that wait is the proof,
	// not a client deadline.
	RequestTimeout time.Duration
}

// New builds an HTTP server with health routes and the standard middleware stack.
func New(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, opts Options) *Server {
	if cfg.AppEnv == "production" || cfg.AppEnv == "test" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	// Do not trust X-Forwarded-For / X-Real-IP unless proxies are configured later.
	_ = engine.SetTrustedProxies(nil)
	mw := []httpx.MiddlewareOption{httpx.WithLogger(log)}
	if opts.RequestTimeout > 0 {
		mw = append(mw, httpx.WithTimeout(opts.RequestTimeout))
	}
	for _, h := range httpx.Standard(mw...) {
		engine.Use(h)
	}

	s := &Server{
		cfg:    cfg,
		pool:   pool,
		engine: engine,
		log:    log,
	}
	s.routes(opts)

	s.http = &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// Engine exposes the Gin engine for tests and future route registration.
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

func (s *Server) routes(opts Options) {
	s.engine.GET("/health", s.handleHealth)
	s.engine.GET("/ready", s.handleReady)
	// Customer API docs are a local development affordance (Day 21). Production
	// keeps the contract in git; it does not ship the interactive explorer.
	if s.cfg.AppEnv == "development" {
		s.engine.GET("/openapi.yaml", s.handleOpenAPISpec)
		s.engine.GET("/docs", s.handleDocs)
	}
	if opts.Auth != nil {
		opts.Auth.Mount(s.engine)
	}
	if opts.Wallets != nil {
		opts.Wallets.Mount(s.engine)
	}
	if opts.Transfers != nil {
		opts.Transfers.Mount(s.engine)
	}
	if opts.KYC != nil {
		opts.KYC.Mount(s.engine)
	}
	if opts.History != nil {
		opts.History.Mount(s.engine)
	}
	if opts.MoneyRequests != nil {
		opts.MoneyRequests.Mount(s.engine)
	}
	s.engine.NoRoute(s.handleNotFound)
}

func (s *Server) handleOpenAPISpec(c *gin.Context) {
	path, err := openapi.SpecPath()
	if err != nil {
		httpx.WriteError(c, httpx.NotFound("OpenAPI spec is not available."))
		return
	}
	c.Header("Content-Type", "application/yaml; charset=utf-8")
	c.File(path)
}

func (s *Server) handleDocs(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_, _ = c.Writer.Write([]byte(docsHTML))
}

// docsHTML is a tiny Redoc shell that loads the local /openapi.yaml.
const docsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>Twofold Customer API</title>
  <style>body { margin: 0; padding: 0; }</style>
</head>
<body>
  <redoc spec-url="/openapi.yaml"></redoc>
  <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
</body>
</html>
`

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "twofold-api",
	})
}

func (s *Server) handleReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := database.Ready(ctx, s.pool); err != nil {
		httpx.WriteError(c, httpx.New(
			http.StatusServiceUnavailable,
			"database_unavailable",
			"Service Unavailable",
			"Database is not ready.",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}

func (s *Server) handleNotFound(c *gin.Context) {
	httpx.WriteError(c, httpx.NotFound("The requested path was not found."))
}

// Run listens until the context is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("http listening", "addr", s.cfg.HTTPAddr)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		s.log.Info("http shutting down")
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}
