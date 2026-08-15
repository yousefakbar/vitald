package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const defaultRedirectURL = "http://127.0.0.1:8765/callback"

type Config struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	TokenPath          string
	RawDataPath        string
	DatabaseURL        string
	Timezone           string
	LogFormat          string
	HTTPTimeout        time.Duration
}

func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve home directory: %w", err)
	}

	cfg := Config{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:  envOr("GOOGLE_REDIRECT_URL", defaultRedirectURL),
		TokenPath:          envOr("VITALD_TOKEN_PATH", filepath.Join(home, ".config", "vitald", "token.json")),
		RawDataPath:        envOr("VITALD_RAW_DATA_PATH", "data/raw"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Timezone:           envOr("VITALD_TIMEZONE", "Asia/Riyadh"),
		LogFormat:          envOr("VITALD_LOG_FORMAT", "text"),
		HTTPTimeout:        60 * time.Second,
	}

	if value := os.Getenv("VITALD_HTTP_TIMEOUT"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse VITALD_HTTP_TIMEOUT: %w", err)
		}
		cfg.HTTPTimeout = d
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("invalid VITALD_TIMEZONE %q: %w", cfg.Timezone, err)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		return Config{}, errors.New("VITALD_LOG_FORMAT must be text or json")
	}
	return cfg, nil
}

func (c Config) ValidateOAuth() error {
	if c.GoogleClientID == "" {
		return errors.New("GOOGLE_CLIENT_ID is required")
	}
	if c.GoogleClientSecret == "" {
		return errors.New("GOOGLE_CLIENT_SECRET is required")
	}
	return nil
}

func (c Config) ValidateRuntime() error {
	if err := c.ValidateOAuth(); err != nil {
		return err
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

// BoolEnv reads a conventional boolean environment variable.
func BoolEnv(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
