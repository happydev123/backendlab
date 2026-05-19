package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/httpapi/apitest"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/testutil"
)

const healthSuite = "version: 1\nname: health-checks\ncases:\n  - id: health\n    method: GET\n    path: /health\n"

func testConfig(t *testing.T) *config.Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "health.yaml"), []byte(healthSuite), 0o644); err != nil {
		t.Fatal(err)
	}
	f := false
	return &config.Server{
		Listen:    "127.0.0.1:0",
		SuitesDir: dir,
		Targets:   map[string]config.Target{"staging": {BaseURL: "https://staging.example.test"}},
		Worker:    config.Worker{MaxReadOnlyAttempts: 2},
		Auth:      config.Auth{SessionTTL: time.Hour, LoginMaxAttempts: 3, LoginWindow: time.Minute, SecureCookies: &f},
		Limits:    limits.Default(),
	}
}

func fakeServer(t *testing.T) (*Server, *apitest.Auth) {
	t.Helper()
	cfg := testConfig(t)
	cat, err := catalog.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := apitest.NewStore()
	au := apitest.NewAuth("operator password 2026", 3)
	s, err := build(httpapi.Deps{Config: cfg, Store: st, Queue: apitest.Queue{Store: st}, Auth: au, Catalog: cat}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return s, au
}

func TestNewValidatesDeps(t *testing.T) {
	cfg := testConfig(t)
	if _, err := New(Deps{}); err == nil {
		t.Error("empty deps accepted")
	}
	if _, err := New(Deps{Config: cfg}); err == nil || !strings.Contains(err.Error(), "database") {
		t.Errorf("missing database: %v", err)
	}
}

func TestAssembledRoutes(t *testing.T) {
	s, au := fakeServer(t)
	h := s.Handler()
	sess := au.NewSession()
	for path, want := range map[string]int{
		"/healthz":        http.StatusOK,
		"/readyz":         http.StatusOK,
		"/api/v1/runs":    http.StatusUnauthorized,
		"/api/v1/nothing": http.StatusNotFound,
		"/runs":           http.StatusSeeOther,
		"/login":          http.StatusOK,
		"/static/app.css": http.StatusOK,
		"/missing":        http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
		if rec.Header().Get("X-Request-Id") == "" || rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: shared middleware missing", path)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookie, Value: sess.Token})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No runs yet.") {
		t.Errorf("signed-in run list: %d", rec.Code)
	}
}

func TestLimitsDefaults(t *testing.T) {
	s, _ := fakeServer(t)
	srv := s.httpServer()
	if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 60*time.Second || srv.WriteTimeout != 60*time.Second ||
		srv.IdleTimeout != 120*time.Second || srv.MaxHeaderBytes != 64<<10 || s.limits.ShutdownTimeout != 15*time.Second {
		t.Errorf("server settings %+v", srv)
	}
	l := Limits{ReadTimeout: time.Second, MaxHeaderBytes: 1}.withDefaults()
	if l.ReadTimeout != time.Second || l.MaxHeaderBytes != 1 || l.WriteTimeout != 60*time.Second {
		t.Errorf("explicit limits overridden: %+v", l)
	}
}

func TestListenAndServeBadAddress(t *testing.T) {
	s, _ := fakeServer(t)
	s.cfg.Listen = "256.256.256.256:1"
	if err := s.ListenAndServe(context.Background()); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("err = %v", err)
	}
}

// slowServer returns a Server whose /slow handler signals started and then
// waits for release (bounded).
func slowServer(t *testing.T, shutdown time.Duration, started chan<- struct{}, release <-chan struct{}) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/fast", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "fast") })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
		}
		io.WriteString(w, "slow done")
	})
	return &Server{cfg: testConfig(t), handler: mux, limits: Limits{ShutdownTimeout: shutdown}.withDefaults(), log: slog.New(slog.DiscardHandler)}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return testutil.WrapListener(ln)
}

func TestServeGracefulShutdown(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s := slowServer(t, 5*time.Second, started, release)
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	// Without keep-alive the client closes each connection after the
	// response, so shutdown does not wait for idle client connections.
	tr := testutil.Transport()
	tr.DisableKeepAlives = true
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	base := "http://" + ln.Addr().String()
	resp, err := client.Get(base + "/fast")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	type result struct {
		body string
		err  error
	}
	slow := make(chan result, 1)
	go func() {
		resp, err := client.Get(base + "/slow")
		if err != nil {
			slow <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		slow <- result{body: string(b), err: err}
	}()
	<-started
	cancel()
	// The in-flight request may finish during shutdown.
	close(release)
	r := <-slow
	if r.err != nil || r.body != "slow done" {
		t.Errorf("in-flight request during shutdown: %q, %v", r.body, r.err)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestServeShutdownTimeout(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s := slowServer(t, 50*time.Millisecond, started, release)
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	client := &http.Client{Transport: testutil.Transport()}
	reqCtx, cancelReq := context.WithCancel(context.Background())
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+ln.Addr().String()+"/slow", nil)
		if resp, err := client.Do(req); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "graceful shutdown") {
		t.Errorf("Serve after shutdown timeout: %v", err)
	}
	close(release)
	cancelReq()
	select {
	case <-reqDone:
	case <-time.After(5 * time.Second):
		t.Error("client request did not finish")
	}
}
