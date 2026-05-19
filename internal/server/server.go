// Package server assembles the HTTP server of "backendlab serve": the
// shared middleware, the JSON API (package httpapi) and the dashboard
// (package dashboard) on one chi router, served by an http.Server with
// explicit timeouts and graceful shutdown.
//
// The server only accepts and displays runs. Runs are executed by
// "backendlab worker" processes (package jobs) that share the database.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/dashboard"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/store"
)

// Limits are the HTTP server bounds. Zero fields take the defaults.
type Limits struct {
	// ReadHeaderTimeout bounds reading request headers (default 10s).
	ReadHeaderTimeout time.Duration
	// ReadTimeout bounds reading a whole request, including uploads
	// (default 60s).
	ReadTimeout time.Duration
	// WriteTimeout bounds writing a response (default 60s).
	WriteTimeout time.Duration
	// IdleTimeout bounds idle keep-alive connections (default 120s).
	IdleTimeout time.Duration
	// MaxHeaderBytes bounds request headers (default 64 KiB).
	MaxHeaderBytes int
	// ShutdownTimeout bounds graceful shutdown; connections still open
	// afterwards are closed (default 15s).
	ShutdownTimeout time.Duration
	// API holds request body limits (see httpapi.Limits).
	API httpapi.Limits
}

func (l Limits) withDefaults() Limits {
	pick := func(v, def time.Duration) time.Duration {
		if v <= 0 {
			return def
		}
		return v
	}
	l.ReadHeaderTimeout = pick(l.ReadHeaderTimeout, 10*time.Second)
	l.ReadTimeout = pick(l.ReadTimeout, 60*time.Second)
	l.WriteTimeout = pick(l.WriteTimeout, 60*time.Second)
	l.IdleTimeout = pick(l.IdleTimeout, 120*time.Second)
	l.ShutdownTimeout = pick(l.ShutdownTimeout, 15*time.Second)
	if l.MaxHeaderBytes <= 0 {
		l.MaxHeaderBytes = 64 << 10
	}
	return l
}

// Deps are the collaborators of the server.
type Deps struct {
	Config  *config.Server
	DB      *store.DB
	Auth    *auth.Service
	Queue   *jobs.Queue
	Catalog *catalog.Catalog
	Logger  *slog.Logger
	Limits  Limits
}

// Server is the assembled HTTP server.
type Server struct {
	cfg     *config.Server
	handler http.Handler
	limits  Limits
	log     *slog.Logger
}

// New checks d and builds the router.
func New(d Deps) (*Server, error) {
	switch {
	case d.Config == nil:
		return nil, errors.New("server: config is required")
	case d.DB == nil:
		return nil, errors.New("server: database is required")
	case d.Auth == nil:
		return nil, errors.New("server: auth service is required")
	case d.Queue == nil:
		return nil, errors.New("server: queue is required")
	case d.Catalog == nil:
		return nil, errors.New("server: catalog is required")
	}
	return build(httpapi.Deps{
		Config:  d.Config,
		Store:   d.DB,
		Queue:   d.Queue,
		Auth:    d.Auth,
		Catalog: d.Catalog,
		Logger:  d.Logger,
		Limits:  d.Limits.API,
	}, d.Limits)
}

// build assembles the router from API dependencies. Tests call it with
// in-memory fakes.
func build(ad httpapi.Deps, limits Limits) (*Server, error) {
	if ad.Logger == nil {
		ad.Logger = slog.New(slog.DiscardHandler)
	}
	api, err := httpapi.New(ad)
	if err != nil {
		return nil, err
	}
	dash, err := dashboard.New(api)
	if err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	r.Use(httpapi.Stack(ad.Logger)...)
	api.Register(r)
	dash.Register(r)
	return &Server{cfg: ad.Config, handler: r, limits: limits.withDefaults(), log: ad.Logger}, nil
}

// Handler returns the root handler with all middleware and routes.
func (s *Server) Handler() http.Handler { return s.handler }
