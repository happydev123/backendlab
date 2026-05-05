package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSecurityHeaders(t *testing.T) {
	log, _ := jsonLogger()
	r := chi.NewRouter()
	r.Use(Stack(log)...)
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("page")) })
	r.Get("/static/x.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write([]byte("css"))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	h := rec.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "script-src 'self'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows inline code: %q", csp)
	}
	for k, v := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if h.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, h.Get(k), v)
		}
	}
	if h.Get(RequestIDHeader) == "" {
		t.Error("Stack does not set a request id")
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/x.css", nil))
	if rec.Header().Get("Cache-Control") != "public, max-age=3600" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("static headers %v", rec.Header())
	}
}

func TestLimitBody(t *testing.T) {
	r := chi.NewRouter()
	var read int
	r.With(LimitBody(10)).Post("/api/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		read = len(data)
		if err != nil {
			WriteError(w, bodyError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.With(LimitBody(10)).Post("/form", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader("0123456789")))
	if rec.Code != http.StatusNoContent || read != 10 {
		t.Errorf("at the limit: %d, read %d", rec.Code, read)
	}

	// Declared length above the limit: rejected before the handler.
	read = -1
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader("0123456789A")))
	if rec.Code != http.StatusRequestEntityTooLarge || read != -1 {
		t.Errorf("declared oversize: %d, handler read %d", rec.Code, read)
	}
	if code, _ := errorCode(t, rec); code != CodeTooLarge {
		t.Errorf("code %s", code)
	}

	// Unknown length (chunked): the reader stops at the limit.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", io.MultiReader(strings.NewReader("0123456789"), strings.NewReader("ABCDEF")))
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("streamed oversize: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(strings.Repeat("x", 11))))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("non-API oversize: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
