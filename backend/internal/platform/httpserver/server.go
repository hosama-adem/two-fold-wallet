package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
)

// Server wraps the HTTP listener and Gin engine.
type Server struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	engine *gin.Engine
	http   *http.Server
	log    *slog.Logger
}

// New builds an HTTP server with health routes registered.
func New(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) *Server {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(requestLogger(log))

	s := &Server{
		cfg:    cfg,
		pool:   pool,
		engine: engine,
		log:    log,
	}
	s.routes()

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

func (s *Server) routes() {
	s.engine.GET("/health", s.handleHealth)
	s.engine.GET("/ready", s.handleReady)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"service": "twofold-api",
	})
}

func (s *Server) handleReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := database.Ready(ctx, s.pool); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"reason": "database_unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
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

func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}
