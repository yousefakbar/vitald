package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/yousefakbar/vitald/internal/archive"
	"github.com/yousefakbar/vitald/internal/config"
	"github.com/yousefakbar/vitald/internal/ingest"
	"github.com/yousefakbar/vitald/internal/provider/googlehealth"
	"github.com/yousefakbar/vitald/internal/storage/postgres"
	"golang.org/x/oauth2"
)

type runtime struct {
	cfg     config.Config
	service *ingest.Service
	store   *postgres.Store
	logger  *slog.Logger
}

func buildRuntime(ctx context.Context) (*runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateRuntime(); err != nil {
		return nil, err
	}
	logger := newLogger(cfg)
	oauthCfg := googlehealth.OAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	tokenStore := googlehealth.TokenStore{Path: cfg.TokenPath}
	token, err := tokenStore.Load()
	if err != nil {
		return nil, err
	}
	source := oauthCfg.TokenSource(ctx, token)
	fresh, err := source.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh OAuth token: %w", err)
	}
	if err := tokenStore.Save(fresh); err != nil {
		return nil, err
	}
	httpClient := oauth2.NewClient(ctx, oauth2.ReuseTokenSource(fresh, source))
	httpClient.Timeout = cfg.HTTPTimeout
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		return nil, err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("load timezone: %w", err)
	}
	service := &ingest.Service{API: googlehealth.NewClient(httpClient), Archive: archive.Filesystem{Root: cfg.RawDataPath}, Store: store, Location: location, Logger: logger}
	return &runtime{cfg: cfg, service: service, store: store, logger: logger}, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	var handler slog.Handler
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, options)
	} else {
		handler = slog.NewTextHandler(os.Stderr, options)
	}
	return slog.New(handler)
}
