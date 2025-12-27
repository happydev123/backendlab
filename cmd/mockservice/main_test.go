package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/testutil"
)

// testListen applies the test listener workaround for hosts where closing
// a listener with a pending accept blocks.
func testListen(network, addr string) (net.Listener, error) {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}
	return testutil.WrapListener(ln), nil
}

func TestRunRejectsBadFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-invoice-total-delta", "five"}, "not a decimal"},
		{[]string{"-invoice-total-delta", "0.001"}, "two decimal places"},
		{[]string{"-max-delay", "0s"}, "must be positive"},
		{[]string{"-max-delay", "2h"}, "must not exceed"},
		{[]string{"extra"}, "unexpected arguments"},
		{[]string{"-no-such-flag"}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		var stderr bytes.Buffer
		if code := run(context.Background(), tt.args, &stderr, testListen); code != 2 {
			t.Errorf("run(%v) = %d, want 2", tt.args, code)
		}
		if !strings.Contains(stderr.String(), tt.want) {
			t.Errorf("run(%v) stderr %q, want %q", tt.args, stderr.String(), tt.want)
		}
	}
}

func TestRunShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	if code := run(ctx, []string{"-addr", "127.0.0.1:0"}, &stderr, testListen); code != 0 {
		t.Fatalf("run = %d, stderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{"mock service listening", "/baseline", "/candidate", "shutting down"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, stderr.String())
		}
	}
}

func TestBuildConfig(t *testing.T) {
	cfg, err := buildConfig("-0.10", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InvoiceTotalDelta.StringFixed(2) != "-0.10" || cfg.MaxDelay != 3*time.Second {
		t.Fatalf("cfg %+v", cfg)
	}
}

func TestLogRequestsKeepsFlusher(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := logRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, "x")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through statusWriter: %v", err)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/baseline/health", nil))
	if !rec.Flushed {
		t.Error("underlying writer was not flushed")
	}
	if !strings.Contains(logs.String(), "status=202") || !strings.Contains(logs.String(), "bytes=1") {
		t.Errorf("access log %q", logs.String())
	}
}

func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.ReadHeaderTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("timeouts not set: %+v", srv)
	}
	if srv.ReadTimeout != 0 {
		t.Fatal("ReadTimeout must stay unset so long handlers are not cancelled")
	}
}
