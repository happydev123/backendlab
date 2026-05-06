package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/httpapi/apitest"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/store"
)

const testPassword = "operator password 2026"

const healthSuite = `version: 1
name: health-checks
cases:
  - id: health
    method: GET
    path: /health
`

const writesSuite = `version: 1
name: counter-writes
cases:
  - id: increment
    method: POST
    path: /counter/increment
`

// env is an API wired to in-memory fakes.
type env struct {
	t     *testing.T
	api   *API
	store *apitest.Store
	auth  *apitest.Auth
	cfg   *config.Server
	logs  *syncBuffer
	dir   string
}

func newEnv(t *testing.T, mods ...func(*config.Server)) *env {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"health.yaml": healthSuite, "writes.yaml": writesSuite} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Server{
		SuitesDir: dir,
		Targets: map[string]config.Target{
			"staging":   {BaseURL: "https://staging.example.test"},
			"candidate": {BaseURL: "https://candidate.example.test"},
		},
		Worker: config.Worker{MaxReadOnlyAttempts: 2},
		Auth:   config.Auth{SessionTTL: time.Hour, LoginMaxAttempts: 3, LoginWindow: 15 * time.Minute},
		Limits: limits.Default(),
	}
	for _, m := range mods {
		m(cfg)
	}
	cat, err := catalog.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := apitest.NewStore()
	st.Migrations = []store.MigrationState{{Version: 1, Name: "create_runs", Applied: true}}
	au := apitest.NewAuth(testPassword, 3)
	log, logs := jsonLogger()
	api, err := New(Deps{Config: cfg, Store: st, Queue: apitest.Queue{Store: st}, Auth: au, Catalog: cat, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, api: api, store: st, auth: au, cfg: cfg, logs: logs, dir: dir}
}

// router returns a chi router with the shared middleware and the routes
// added by register.
func (e *env) router(register func(r chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Use(Stack(e.api.Logger())...)
	register(r)
	return r
}

type reqOpt func(*http.Request)

func withCookie(s *auth.Session) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookie, Value: s.Token}) }
}

func withBearer(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func withCSRF(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set(CSRFHeader, token) }
}

func withHeader(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func withRemote(addr string) reqOpt {
	return func(r *http.Request) { r.RemoteAddr = addr }
}

// serve sends a request to h. A non-empty body is sent as JSON unless an
// option overrides the content type.
func serve(h http.Handler, method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// expectError checks status and envelope code.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	got, msg := errorCode(t, rec)
	if got != code {
		t.Fatalf("code = %q, want %q (message %q)", got, code, msg)
	}
	return msg
}

// apiStore shortens the fake store type in table tests.
type apiStore = apitest.Store

// The production types satisfy the interfaces.
var (
	_ Store         = (*store.DB)(nil)
	_ Canceller     = (*jobs.Queue)(nil)
	_ Authenticator = (*auth.Service)(nil)
)
