// Package config loads and validates Inkwell's runtime configuration.
//
// Configuration is read from environment variables. A sibling .env file is
// supported by the user (loaded via their shell or docker compose); this
// package makes no assumptions about how env vars arrive.
package config

import (
	"os"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

// Config holds all runtime configuration for Inkwell.
type Config struct {
	// AnthropicAPIKey authenticates requests to api.anthropic.com.
	AnthropicAPIKey string

	// ListenAddr is the address the HTTP server binds to (e.g. ":8080").
	ListenAddr string

	// DatabaseURL is a SQLite URI — a file path or "file:..." URI.
	// See https://www.sqlite.org/uri.html for URI options.
	DatabaseURL string

	// LogLevel controls zerolog verbosity.
	LogLevel zerolog.Level

	// WebDir is the filesystem path to static frontend assets.
	WebDir string
}

// Load reads configuration from the environment, applies defaults, and
// returns an error if any required value is missing.
func Load() (*Config, error) {
	c := &Config{
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		ListenAddr:      ":" + getenv("PORT", "8080"),
		DatabaseURL:     getenv("DB_PATH", "inkwell.db"),
		WebDir:          getenv("WEB_DIR", "web"),
		LogLevel:        parseLevel(getenv("LOG_LEVEL", "info")),
	}

	if c.AnthropicAPIKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is required")
	}

	return c, nil
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func parseLevel(s string) zerolog.Level {
	l, err := zerolog.ParseLevel(s)
	if err != nil || l == zerolog.NoLevel {
		return zerolog.InfoLevel
	}
	return l
}
