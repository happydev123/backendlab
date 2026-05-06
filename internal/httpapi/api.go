package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/migrations"
)

// Store is the subset of *store.DB the HTTP layer uses. It exists so
// handler tests can run without PostgreSQL; production passes *store.DB.
type Store interface {
	Ping(ctx context.Context) error
	MigrationStatus(ctx context.Context, fsys fs.FS) ([]store.MigrationState, error)
	EnqueueRun(ctx context.Context, n store.NewRun) (*store.Run, error)
	GetRun(ctx context.Context, publicID string) (*store.Run, error)
	GetRunByID(ctx context.Context, id int64) (*store.Run, error)
	ListRuns(ctx context.Context, p store.ListRunsParams) ([]store.RunSummary, int64, error)
	ListCaseResults(ctx context.Context, runID int64) ([]store.CaseResult, error)
	GetReport(ctx context.Context, runID int64) ([]byte, error)
	Resubmit(ctx context.Context, runID int64) (*store.Run, error)
}

// Canceller requests run cancellation; production passes *jobs.Queue.
type Canceller interface {
	RequestCancel(ctx context.Context, runID int64) (store.Status, error)
}

// Authenticator manages operator sessions; production passes
// *auth.Service.
type Authenticator interface {
	Login(ctx context.Context, password, throttleKey string) (*auth.Session, error)
	Authenticate(ctx context.Context, token string) (*auth.Session, error)
	Logout(ctx context.Context, token string) error
}

// Limits bounds request handling. Zero fields take the defaults below.
type Limits struct {
	// MaxJSONBytes bounds JSON request bodies (default 64 KiB).
	MaxJSONBytes int64
	// MaxFormBytes bounds URL-encoded dashboard forms (default 64 KiB).
	MaxFormBytes int64
	// MaxPlanBytes bounds each uploaded plan file (default: the server
	// limits.max_plan_bytes). The whole upload may be twice this plus a
	// small allowance for multipart framing.
	MaxPlanBytes int64
	// ReadyTimeout bounds the readiness check (default 2s).
	ReadyTimeout time.Duration
}

// smallBodyBytes bounds bodies of endpoints that take no input (cancel,
// resubmit, logout); clients may still send an empty JSON object.
const smallBodyBytes = 1 << 10

// multipartOverhead is allowed on top of two plan files for part headers
// and the small text fields.
const multipartOverhead = 64 << 10

func (l Limits) withDefaults(cfg *config.Server) Limits {
	if l.MaxJSONBytes <= 0 {
		l.MaxJSONBytes = 64 << 10
	}
	if l.MaxFormBytes <= 0 {
		l.MaxFormBytes = 64 << 10
	}
	if l.MaxPlanBytes <= 0 {
		l.MaxPlanBytes = cfg.Limits.Merge().MaxPlanBytes
	}
	if l.ReadyTimeout <= 0 {
		l.ReadyTimeout = 2 * time.Second
	}
	return l
}

// MaxUploadBytes is the bound on a whole plan comparison upload.
func (l Limits) MaxUploadBytes() int64 { return 2*l.MaxPlanBytes + multipartOverhead }

// Deps are the collaborators of the API.
type Deps struct {
	Config  *config.Server
	Store   Store
	Queue   Canceller
	Auth    Authenticator
	Catalog *catalog.Catalog
	// Migrations is the migration set readiness compares against; nil
	// means the embedded migrations.FS.
	Migrations fs.FS
	Logger     *slog.Logger
	Limits     Limits
}

// API serves the JSON API and provides the operations the dashboard
// reuses.
type API struct {
	cfg        *config.Server
	store      Store
	queue      Canceller
	auth       Authenticator
	catalog    *catalog.Catalog
	migrations fs.FS
	log        *slog.Logger
	limits     Limits
}

// New checks d and returns an API.
func New(d Deps) (*API, error) {
	switch {
	case d.Config == nil:
		return nil, errors.New("httpapi: config is required")
	case d.Store == nil:
		return nil, errors.New("httpapi: store is required")
	case d.Queue == nil:
		return nil, errors.New("httpapi: queue is required")
	case d.Auth == nil:
		return nil, errors.New("httpapi: auth service is required")
	case d.Catalog == nil:
		return nil, errors.New("httpapi: catalog is required")
	}
	if d.Migrations == nil {
		d.Migrations = migrations.FS
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &API{
		cfg:        d.Config,
		store:      d.Store,
		queue:      d.Queue,
		auth:       d.Auth,
		catalog:    d.Catalog,
		migrations: d.Migrations,
		log:        d.Logger,
		limits:     d.Limits.withDefaults(d.Config),
	}, nil
}

// Config returns the server configuration.
func (a *API) Config() *config.Server { return a.cfg }

// Catalog returns the approved suites.
func (a *API) Catalog() *catalog.Catalog { return a.catalog }

// Limits returns the effective request limits.
func (a *API) Limits() Limits { return a.limits }

// Logger returns the base logger.
func (a *API) Logger() *slog.Logger { return a.log }

// logFor returns the logger with the request ID of ctx.
func (a *API) logFor(ctx context.Context) *slog.Logger { return RequestLogger(ctx, a.log) }

// fail logs err as an internal failure of op and returns the generic 500.
func (a *API) fail(ctx context.Context, op string, err error) *Error {
	a.logFor(ctx).Error("request failed", slog.String("op", op), slog.String("error", a.cfg.Redactor().Error(err)))
	return internalErr()
}
