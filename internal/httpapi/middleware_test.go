package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// logLines decodes JSON log lines.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func jsonLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// errorCode decodes the error envelope of rec.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var env struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("status %d: body %q is not an error envelope: %v", rec.Code, rec.Body.String(), err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("error content type %q", ct)
	}
	return env.Error.Code, env.Error.Message
}

func middlewareRouter(log *slog.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(RequestID, AccessLog(log), Recover(log))
	r.Get("/api/v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		RequestLogger(r.Context(), log).Info("handler ran")
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("ok"))
	})
	r.Get("/api/v1/panic", func(http.ResponseWriter, *http.Request) { panic("secret-in-panic") })
	r.Get("/page/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	r.Get("/api/v1/late-panic", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("after header")
	})
	return r
}

func TestRequestIDAssignedAndPropagated(t *testing.T) {
	log, buf := jsonLogger()
	h := middlewareRouter(log)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/things/42", nil))
	id := rec.Header().Get(RequestIDHeader)
	if !strings.HasPrefix(id, "req_") || len(id) != 24 {
		t.Fatalf("assigned request id %q", id)
	}
	lines := buf.lines(t)
	if len(lines) != 2 {
		t.Fatalf("log lines = %v", lines)
	}
	if lines[0]["msg"] != "handler ran" || lines[0]["request_id"] != id {
		t.Errorf("handler log = %v", lines[0])
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/things/42", nil))
	if rec2.Header().Get(RequestIDHeader) == id {
		t.Error("request ids repeat")
	}
}

func TestRequestIDClientValuesAreFiltered(t *testing.T) {
	log, _ := jsonLogger()
	h := middlewareRouter(log)
	for value, kept := range map[string]bool{
		"trace-1234.abc_DEF":                 true,
		"short":                              false,
		"has space in it":                    false,
		"newline\ninjected":                  false,
		`quote"inject`:                       false,
		strings.Repeat("a", 65):              false,
		"0123456789abcdef0123456789abcdef01": true,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things/1", nil)
		req.Header.Set(RequestIDHeader, value)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get(RequestIDHeader)
		if (got == value) != kept {
			t.Errorf("client id %q: response id %q, kept=%v", value, got, got == value)
		}
	}
}

func TestAccessLogFields(t *testing.T) {
	log, buf := jsonLogger()
	h := middlewareRouter(log)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things/42?token=query-secret", nil)
	req.Header.Set("Authorization", "Bearer header-secret")
	req.Header.Set("Cookie", "backendlab_session=cookie-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	lines := buf.lines(t)
	access := lines[len(lines)-1]
	want := map[string]any{"msg": "http request", "method": "GET", "route": "/api/v1/things/{id}", "status": float64(418), "bytes": float64(2)}
	for k, v := range want {
		if access[k] != v {
			t.Errorf("access log %s = %v, want %v", k, access[k], v)
		}
	}
	if access["request_id"] != rec.Header().Get(RequestIDHeader) {
		t.Errorf("access log request_id = %v", access["request_id"])
	}
	if _, ok := access["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing: %v", access)
	}
	for _, secret := range []string{"query-secret", "header-secret", "cookie-secret", "/things/42"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log contains %q", secret)
		}
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	lines = buf.lines(t)
	if lines[len(lines)-1]["route"] != "unmatched" || lines[len(lines)-1]["status"] != float64(404) {
		t.Errorf("unmatched route log = %v", lines[len(lines)-1])
	}
}

func TestRecoverAnswers500WithoutStack(t *testing.T) {
	log, buf := jsonLogger()
	h := middlewareRouter(log)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	code, msg := errorCode(t, rec)
	if code != CodeInternal || strings.Contains(rec.Body.String(), "secret-in-panic") || strings.Contains(rec.Body.String(), "goroutine") {
		t.Errorf("panic response %q %q", code, msg)
	}
	logs := buf.String()
	if !strings.Contains(logs, "panic while serving request") || !strings.Contains(logs, "secret-in-panic") || !strings.Contains(logs, "goroutine") {
		t.Errorf("panic not logged with stack: %s", logs)
	}
	// The access log records the recovered panic as 500.
	lines := buf.lines(t)
	if last := lines[len(lines)-1]; last["status"] != float64(500) || last["level"] != "ERROR" {
		t.Errorf("access log after panic = %v", last)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/page/panic", nil))
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("page panic: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestRecoverAbortsStartedResponse(t *testing.T) {
	log, _ := jsonLogger()
	h := middlewareRouter(log)
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/late-panic", nil))
}

func TestDecodeJSON(t *testing.T) {
	type body struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	cases := []struct {
		ctype, body string
		status      int
		code        string
	}{
		{"application/json", `{"name":"a","n":1}`, 0, ""},
		{"application/json; charset=utf-8", `{"name":"a"}`, 0, ""},
		{"text/plain", `{"name":"a"}`, 415, CodeUnsupportedMedia},
		{"", `{"name":"a"}`, 415, CodeUnsupportedMedia},
		{"application/json", ``, 400, CodeInvalidRequest},
		{"application/json", `{"name":`, 400, CodeInvalidRequest},
		{"application/json", `{"name":"a","url":"http://x"}`, 400, CodeInvalidRequest},
		{"application/json", `{"n":"one"}`, 400, CodeInvalidRequest},
		{"application/json", `{"name":"a"} {"name":"b"}`, 400, CodeInvalidRequest},
		{"application/json", `{"name":"` + strings.Repeat("x", 200) + `"}`, 413, CodeTooLarge},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.ctype)
		req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 100)
		var dst body
		e := decodeJSON(req, &dst)
		switch {
		case tc.status == 0 && e != nil:
			t.Errorf("%q: unexpected error %v", tc.body, e)
		case tc.status != 0 && (e == nil || e.Status != tc.status || e.Code != tc.code):
			t.Errorf("%q (%s): got %v, want %d %s", tc.body, tc.ctype, e, tc.status, tc.code)
		}
	}
}

func TestWriteErrorRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, &Error{Status: 429, Code: CodeThrottled, Message: "slow down", RetryAfter: 1500 * 1e6})
	if rec.Header().Get("Retry-After") != "2" || rec.Code != 429 {
		t.Errorf("retry-after %q status %d", rec.Header().Get("Retry-After"), rec.Code)
	}
	if code, msg := errorCode(t, rec); code != CodeThrottled || msg != "slow down" {
		t.Errorf("envelope %s %s", code, msg)
	}
}
