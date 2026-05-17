package dashboard

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/httpapi/apitest"
	"github.com/happydev123/backendlab/internal/limits"
)

const testPassword = "operator password 2026"

// denv is a dashboard wired to in-memory fakes.
type denv struct {
	dash  *Dashboard
	api   *httpapi.API
	store *apitest.Store
	auth  *apitest.Auth
	cfg   *config.Server
	sess  *auth.Session
}

func newDenv(t *testing.T, mods ...func(*config.Server)) *denv {
	t.Helper()
	dir := t.TempDir()
	suites := map[string]string{
		"health.yaml": "version: 1\nname: health-checks\ndescription: Read-only checks.\ncases:\n  - id: health\n    method: GET\n    path: /health\n",
		"writes.yaml": "version: 1\nname: counter-writes\ncases:\n  - id: increment\n    method: POST\n    path: /counter\n",
		"broken.yaml": "version: 1\nname: broken\ncases: <script>\n",
	}
	for name, content := range suites {
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
		Auth:   config.Auth{SessionTTL: time.Hour, LoginMaxAttempts: 2, LoginWindow: time.Minute},
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
	au := apitest.NewAuth(testPassword, 2)
	api, err := httpapi.New(httpapi.Deps{Config: cfg, Store: st, Queue: apitest.Queue{Store: st}, Auth: au, Catalog: cat,
		Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	dash, err := New(api)
	if err != nil {
		t.Fatal(err)
	}
	return &denv{dash: dash, api: api, store: st, auth: au, cfg: cfg, sess: au.NewSession()}
}

// mux builds a router with the shared middleware and the given routes.
func (e *denv) mux(register func(r chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Use(httpapi.Stack(e.api.Logger())...)
	register(r)
	return r
}

type opt func(*http.Request)

func asOperator(s *auth.Session) opt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: httpapi.SessionCookie, Value: s.Token}) }
}

func get(h http.Handler, path string, opts ...opt) *httptest.ResponseRecorder {
	return do(h, http.MethodGet, path, "", "", opts...)
}

// postForm submits URL-encoded values.
func postForm(h http.Handler, path string, values url.Values, opts ...opt) *httptest.ResponseRecorder {
	return do(h, http.MethodPost, path, values.Encode(), "application/x-www-form-urlencoded", opts...)
}

func do(h http.Handler, method, path, body, ctype string, opts ...opt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func expectPage(t *testing.T, rec *httptest.ResponseRecorder, status int, contains ...string) string {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d; body:\n%s", rec.Code, status, rec.Body.String())
	}
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type %q", ct)
	}
	for _, c := range contains {
		if !strings.Contains(body, c) {
			t.Errorf("page lacks %q", c)
		}
	}
	return body
}

func expectRedirect(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Fatalf("got %d Location %q, want 303 to %q; body %s", rec.Code, rec.Header().Get("Location"), to, rec.Body.String())
	}
}
