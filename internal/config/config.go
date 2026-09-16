// Package config loads application configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved application configuration.
type Config struct {
	App App
	DB  DB
	AI  AI
}

// App holds HTTP server and session settings.
type App struct {
	Addr            string
	Env             string
	LogLevel        string
	SessionSecret   string
	SessionTTL      time.Duration
	MaxUploadBytes  int64
	ShutdownTimeout time.Duration
	RunMigrations   bool

	// BootstrapUser and BootstrapPassword create the first account when the
	// database has no users. Both are ignored once any account exists, so they
	// cannot be used to reset a password later. Leaving them unset shows the
	// first-run setup screen instead.
	BootstrapUser     string
	BootstrapPassword string
}

// DB holds PostgreSQL connection settings.
type DB struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// AI holds provider-agnostic AI reviewer settings. Provider selects which
// concrete implementation of service.AIReviewer is constructed at wiring time.
type AI struct {
	// Provider is one of: openaicompat, anthropic.
	Provider string
	// BaseURL is the provider root, e.g. https://openwebui.internal/api
	// for an Open WebUI deployment, or https://api.openai.com/v1.
	BaseURL string
	// ChatPath is appended to BaseURL for the chat completion call. Open WebUI
	// and OpenAI both use /chat/completions; kept configurable because some
	// gateways mount it elsewhere.
	ChatPath       string
	APIKey         string
	Model          string
	MaxTokens      int
	Temperature    float64
	Timeout        time.Duration
	MaxRetries     int
	BatchSize      int
	MaxConcurrency int
}

const (
	ProviderOpenAICompat = "openaicompat"
	ProviderAnthropic    = "anthropic"
)

// Load reads configuration from the environment, applying defaults, and
// validates it. It returns a descriptive error naming the offending variable
// rather than panicking, so startup failures are diagnosable from logs.
func Load() (*Config, error) {
	cfg := &Config{
		App: App{
			Addr:              env("APP_ADDR", ":8080"),
			Env:               env("APP_ENV", "development"),
			LogLevel:          env("LOG_LEVEL", "info"),
			SessionSecret:     env("SESSION_SECRET", ""),
			SessionTTL:        envDuration("SESSION_TTL", 12*time.Hour),
			MaxUploadBytes:    int64(envInt("MAX_UPLOAD_MB", 25)) << 20,
			ShutdownTimeout:   envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
			RunMigrations:     envBool("RUN_MIGRATIONS", true),
			BootstrapUser:     env("BOOTSTRAP_USERNAME", ""),
			BootstrapPassword: env("BOOTSTRAP_PASSWORD", ""),
		},
		DB: DB{
			DSN:             env("DATABASE_URL", ""),
			MaxConns:        int32(envInt("DB_MAX_CONNS", 10)),
			MinConns:        int32(envInt("DB_MIN_CONNS", 1)),
			MaxConnLifetime: envDuration("DB_MAX_CONN_LIFETIME", time.Hour),
			ConnectTimeout:  envDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
		},
		AI: AI{
			Provider:       strings.ToLower(env("AI_PROVIDER", "")),
			BaseURL:        strings.TrimRight(env("AI_BASE_URL", ""), "/"),
			ChatPath:       env("AI_CHAT_PATH", "/chat/completions"),
			APIKey:         env("AI_API_KEY", ""),
			Model:          env("AI_MODEL", ""),
			MaxTokens:      envInt("AI_MAX_TOKENS", 4096),
			Temperature:    envFloat("AI_TEMPERATURE", 0.2),
			Timeout:        envDuration("AI_TIMEOUT", 120*time.Second),
			MaxRetries:     envInt("AI_MAX_RETRIES", 2),
			BatchSize:      envInt("AI_BATCH_SIZE", 8),
			MaxConcurrency: envInt("AI_MAX_CONCURRENCY", 2),
		},
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DB.DSN == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if c.App.SessionSecret == "" {
		if c.App.Env == "production" {
			return fmt.Errorf("config: SESSION_SECRET is required when APP_ENV=production")
		}
		// Dev convenience only; never silently used in production.
		c.App.SessionSecret = "dev-insecure-session-secret-change-me"
	}
	switch c.AI.Provider {
	case ProviderOpenAICompat, ProviderAnthropic:
		if c.AI.BaseURL == "" {
			return fmt.Errorf("config: AI_BASE_URL is required for AI_PROVIDER=%s", c.AI.Provider)
		}
		if c.AI.Model == "" {
			return fmt.Errorf("config: AI_MODEL is required for AI_PROVIDER=%s", c.AI.Provider)
		}
	case "":
		return fmt.Errorf("config: AI_PROVIDER is required (openaicompat or anthropic)")
	default:
		return fmt.Errorf("config: unknown AI_PROVIDER %q (want openaicompat or anthropic)", c.AI.Provider)
	}
	if c.AI.BatchSize < 1 {
		return fmt.Errorf("config: AI_BATCH_SIZE must be >= 1")
	}
	if c.AI.MaxConcurrency < 1 {
		return fmt.Errorf("config: AI_MAX_CONCURRENCY must be >= 1")
	}
	if c.DB.MinConns > c.DB.MaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS (%d) exceeds DB_MAX_CONNS (%d)", c.DB.MinConns, c.DB.MaxConns)
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}
