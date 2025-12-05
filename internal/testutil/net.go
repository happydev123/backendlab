// Package testutil contains helpers shared by tests. It is imported only
// from _test.go files and the demonstration scenario.
package testutil

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// EnvAsyncClose turns on a workaround for hosts where closing a socket that
// has a pending read or accept blocks indefinitely (observed on Windows
// with some VPN layered service providers). With it set to "1", test
// listeners and client connections close in the background, so
// httptest.Server.Close and client timeouts do not stall the test binary.
// Production code never reads it.
const EnvAsyncClose = "BACKENDLAB_TEST_ASYNC_CLOSE"

// AsyncClose reports whether the workaround is enabled.
func AsyncClose() bool { return os.Getenv(EnvAsyncClose) == "1" }

type asyncConn struct{ net.Conn }

func (c asyncConn) Close() error {
	go c.Conn.Close() //nolint:errcheck // close result is irrelevant in tests
	return nil
}

type acceptResult struct {
	conn net.Conn
	err  error
}

// asyncListener accepts in a goroutine so Close returns immediately even
// if the platform never completes the pending accept.
type asyncListener struct {
	net.Listener
	results chan acceptResult
	closed  chan struct{}
	once    sync.Once
}

func newAsyncListener(l net.Listener) *asyncListener {
	al := &asyncListener{Listener: l, results: make(chan acceptResult), closed: make(chan struct{})}
	go func() {
		for {
			c, err := l.Accept()
			select {
			case al.results <- acceptResult{conn: c, err: err}:
			case <-al.closed:
				if c != nil {
					go c.Close() //nolint:errcheck
				}
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return al
}

func (l *asyncListener) Accept() (net.Conn, error) {
	select {
	case r := <-l.results:
		if r.err != nil {
			return nil, r.err
		}
		return asyncConn{r.conn}, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *asyncListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		go l.Listener.Close() //nolint:errcheck
	})
	return nil
}

// WrapListener applies the workaround to l when enabled.
func WrapListener(l net.Listener) net.Listener {
	if !AsyncClose() {
		return l
	}
	return newAsyncListener(l)
}

// DialContext returns a dial function applying the workaround, or nil when
// it is disabled so callers keep their default dialer.
func DialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	if !AsyncClose() {
		return nil
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return asyncConn{c}, nil
	}
}

// WrapConn applies the workaround to an established connection when
// enabled and returns c unchanged otherwise.
func WrapConn(c net.Conn) net.Conn {
	if !AsyncClose() {
		return c
	}
	return asyncConn{c}
}

// NewServer starts an httptest server that is closed when the test ends
// without blocking on the platform quirk.
func NewServer(t testing.TB, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = WrapListener(srv.Listener)
	srv.Start()
	t.Cleanup(func() {
		if AsyncClose() {
			go srv.Close()
			return
		}
		srv.Close()
	})
	return srv
}

// Transport returns a fresh transport using the workaround dialer when
// enabled. Tests pass it to code under test instead of
// http.DefaultTransport so connections are not shared between tests.
func Transport() *http.Transport {
	tr := &http.Transport{MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second}
	if d := DialContext(); d != nil {
		tr.DialContext = d
	}
	return tr
}

// Release returns a channel that is closed when the test ends. Slow test
// handlers wait on it (as well as on a bounded timer) instead of sleeping,
// so they finish promptly once the test is done.
func Release(t testing.TB) <-chan struct{} {
	ch := make(chan struct{})
	t.Cleanup(func() { close(ch) })
	return ch
}
