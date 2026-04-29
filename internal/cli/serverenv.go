package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

// EnvServerConfig names the variable consulted when --config is omitted
// by server-side commands.
const EnvServerConfig = "BACKENDLAB_CONFIG"

// serverEnv is the configuration, logger and database shared by serve,
// worker, migrate and the administrative commands.
type serverEnv struct {
	cfg *config.Server
	log *slog.Logger
	db  *store.DB
}

func (a *App) serverConfigPath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v, ok := a.lookupEnv(EnvServerConfig); ok && v != "" {
		return v, nil
	}
	return "", usagef("--config (or %s) is required for server commands", EnvServerConfig)
}

// openServerEnv loads the server configuration and connects to
// PostgreSQL. The database URL is read from the environment variable named
// in the configuration and is never logged.
func (a *App) openServerEnv(ctx context.Context, configFlag string) (*serverEnv, error) {
	path, err := a.serverConfigPath(configFlag)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadServer(path)
	if err != nil {
		return nil, err
	}
	log := a.serverLogger(cfg.Log)
	url, ok := a.lookupEnv(cfg.Database.URLEnv)
	if !ok || strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("environment variable %s (database URL) is not set", cfg.Database.URLEnv)
	}
	db, err := store.Open(ctx, url, cfg.Database)
	if err != nil {
		// pgx errors can include the connection string; report only that
		// the connection failed and the variable used.
		return nil, fmt.Errorf("connect to PostgreSQL using %s: %s", cfg.Database.URLEnv, cfg.Redactor().WithSecrets(url).Error(err))
	}
	return &serverEnv{cfg: cfg, log: log, db: db}, nil
}

// serverLogger builds the structured logger for server processes. Logs go
// to stderr; reports and API responses never pass through it.
func (a *App) serverLogger(lc config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch lc.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if lc.Format == "text" {
		return slog.New(slog.NewTextHandler(a.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(a.Stderr, opts))
}

// hostname is used in worker identifiers.
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}

// envLookup returns the App environment, falling back to the process
// environment.
func (a *App) envLookup() config.EnvLookup {
	if a.Env != nil {
		return a.Env
	}
	return config.OSEnv
}
