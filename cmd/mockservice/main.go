// Command mockservice runs the BackendLab demonstration service: one
// process serving a baseline and a candidate variant of a small invoice and
// streaming API under /baseline and /candidate. It exists for the examples
// and integration tests and contains development fault controls (slow
// responses, broken streams, a counter reset endpoint). It listens on
// loopback by default and must never be exposed on a shared network.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/mock"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr, net.Listen))
}

// listenFunc opens the listening socket; tests substitute a wrapper.
type listenFunc func(network, addr string) (net.Listener, error)

// run parses flags, serves until ctx is cancelled, and returns the exit
// code: 0 after a clean shutdown, 1 on a runtime failure, 2 on bad usage.
func run(ctx context.Context, args []string, stderr io.Writer, listen listenFunc) int {
	fs := flag.NewFlagSet("mockservice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", mock.DefaultAddr, "listen address; keep it on loopback")
	delta := fs.String("invoice-total-delta", mock.DefaultInvoiceTotalDelta,
		"decimal amount added to the candidate invoice total (at most two decimal places)")
	maxDelay := fs.Duration("max-delay", mock.DefaultMaxDelay, "cap for requested delays and stream pauses")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mockservice: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	cfg, err := buildConfig(*delta, *maxDelay)
	if err != nil {
		fmt.Fprintf(stderr, "mockservice: %v\n", err)
		return 2
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ln, err := listen("tcp", *addr)
	if err != nil {
		logger.Error("listen failed", "addr", *addr, "err", err)
		return 1
	}
	if !isLoopback(ln.Addr()) {
		logger.Warn("listening on a non-loopback address; the mock has unauthenticated control endpoints",
			"addr", ln.Addr().String())
	}

	srv := newHTTPServer(mock.New(cfg).Handler(), logger)
	logger.Info("mock service listening",
		"addr", ln.Addr().String(),
		"baseline", "http://"+ln.Addr().String()+"/baseline",
		"candidate", "http://"+ln.Addr().String()+"/candidate",
		"invoice_total_delta", cfg.InvoiceTotalDelta.StringFixed(2),
		"max_delay", cfg.MaxDelay.String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		logger.Error("server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	code := 0
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error("graceful shutdown incomplete", "err", err)
		_ = srv.Close()
		code = 1
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		code = 1
	}
	return code
}

// buildConfig turns flag values into a validated mock configuration.
func buildConfig(delta string, maxDelay time.Duration) (mock.Config, error) {
	d, err := decimal.NewFromString(delta)
	if err != nil {
		return mock.Config{}, fmt.Errorf("-invoice-total-delta %q is not a decimal number", delta)
	}
	if maxDelay <= 0 {
		return mock.Config{}, errors.New("-max-delay must be positive")
	}
	cfg := mock.Config{InvoiceTotalDelta: d, MaxDelay: maxDelay}
	if err := cfg.Validate(); err != nil {
		return mock.Config{}, err
	}
	return cfg, nil
}

// newHTTPServer configures timeouts. WriteTimeout bounds ordinary
// responses; the slow, stream and counter handlers extend their own write
// deadline through http.ResponseController by their capped delay, so the
// server-wide value does not cut streams short. ReadTimeout is left unset
// on purpose: in net/http it also bounds the background read that detects
// client disconnects, and expiring it would cancel long-running handlers.
// Requests carry no bodies worth reading, so ReadHeaderTimeout suffices.
func newHTTPServer(h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           logRequests(logger, h),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// statusWriter records the response status for the access log. Unwrap
// lets http.ResponseController reach the underlying writer for flushing
// and deadlines.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests writes one access log line per request.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"client_gone", r.Context().Err() != nil)
	})
}
