package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
)

// ListenAndServe listens on the configured address (listen in the server
// file) and serves until ctx is cancelled; see Serve.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", s.cfg.Listen, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves HTTP on ln until ctx is cancelled, then shuts down: the
// listener closes, idle connections are closed, and in-flight requests get
// up to Limits.ShutdownTimeout to finish before their connections are
// closed forcibly. It returns nil after a clean shutdown and an error if
// serving failed or shutdown timed out. Serve does not return before the
// serving goroutine has exited.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := s.httpServer()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	s.log.Info("server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-errc:
		return fmt.Errorf("server: serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("server shutting down", slog.Duration("timeout", s.limits.ShutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.limits.ShutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		// In-flight requests did not finish in time; drop them.
		srv.Close() //nolint:errcheck // the shutdown error is reported
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: serve: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("server: graceful shutdown: %w", shutdownErr)
	}
	s.log.Info("server stopped")
	return nil
}

// httpServer returns the http.Server with the configured timeouts.
func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: s.limits.ReadHeaderTimeout,
		ReadTimeout:       s.limits.ReadTimeout,
		WriteTimeout:      s.limits.WriteTimeout,
		IdleTimeout:       s.limits.IdleTimeout,
		MaxHeaderBytes:    s.limits.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
}
