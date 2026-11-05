package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is process-wide configuration loaded from the environment.
type Config struct {
	AppEnv             string
	HTTPAddr           string
	DatabaseURL        string
	JWTSigningKey      string
	JWTKid             string
	StaffJWTSigningKey string
	AccessTokenTTL     time.Duration
	RefreshTokenTTL    time.Duration
	ShutdownTimeout    time.Duration
	DBMaxConns         int32
	DBMinConns         int32
}

// Load reads configuration from the environment.
// An optional .env file is loaded when present (local development).
func Load() (Config, error) {
	// Prefer process env (Makefile exports .env). Fall back to common local paths.
	_ = godotenv.Load()
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	cfg := Config{
		AppEnv:             getenv("APP_ENV", "development"),
		HTTPAddr:           getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		JWTSigningKey:      normalizePEM(os.Getenv("JWT_SIGNING_KEY")),
		JWTKid:             getenv("JWT_KID", "dev-1"),
		StaffJWTSigningKey: os.Getenv("STAFF_JWT_SIGNING_KEY"),
		AccessTokenTTL:     durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:    durationEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		ShutdownTimeout:    durationEnv("SHUTDOWN_TIMEOUT", 10*time.Second),
		DBMaxConns:         int32Env("DB_MAX_CONNS", 10),
		DBMinConns:         int32Env("DB_MIN_CONNS", 1),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSigningKey == "" {
		return Config{}, fmt.Errorf("JWT_SIGNING_KEY is required")
	}
	if cfg.StaffJWTSigningKey == "" {
		return Config{}, fmt.Errorf("STAFF_JWT_SIGNING_KEY is required")
	}

	return cfg, nil
}

// normalizePEM turns env-friendly PEM blobs into parseable PEM text.
// Supports quoted values and literal "\n" sequences from single-line .env entries.
func normalizePEM(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		}
	}
	s = strings.ReplaceAll(s, `\n`, "\n")
	return strings.TrimSpace(s)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func int32Env(key string, fallback int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return int32(n)
}
