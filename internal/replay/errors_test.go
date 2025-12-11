package replay

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/redact"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()

	urlErr := func(err error) error { return &url.Error{Op: "Get", URL: "http://h/x", Err: err} }
	cases := []struct {
		name string
		err  error
		cx   contexts
		want string
	}{
		{"parent cancelled", urlErr(context.Canceled), contexts{cancelled, live, live}, CodeCancelled},
		{"parent deadline", urlErr(context.DeadlineExceeded), contexts{expired, live, live}, CodeRunTimeout},
		{"run timeout", urlErr(context.DeadlineExceeded), contexts{live, expired, expired}, CodeRunTimeout},
		{"request timeout", urlErr(context.DeadlineExceeded), contexts{live, live, expired}, CodeTimeout},
		{"net timeout", urlErr(&net.OpError{Op: "dial", Err: timeoutErr{}}), contexts{live, live, live}, CodeTimeout},
		{"redirects", urlErr(errTooManyRedirects), contexts{live, live, live}, CodeTooManyRedirects},
		{"blocked", urlErr(&redirectBlockedError{err: errors.New("denied")}), contexts{live, live, live}, CodeRedirectBlocked},
		{"too large", fmt.Errorf("read: %w", errResponseTooLarge), contexts{live, live, live}, CodeResponseTooLarge},
		{"tls", urlErr(x509.UnknownAuthorityError{}), contexts{live, live, live}, CodeTLSFailed},
		{"refused", urlErr(&net.OpError{Op: "dial", Err: errors.New("connection refused")}), contexts{live, live, live}, CodeConnectionFailed},
		{"eof", urlErr(io.EOF), contexts{live, live, live}, CodeConnectionFailed},
		{"other", errors.New("strange"), contexts{live, live, live}, CodeRequestFailed},
	}
	for _, c := range cases {
		if got := classify(c.err, c.cx); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestTransportMessage(t *testing.T) {
	r := redact.Default().WithSecrets("s3cret-value")
	err := &url.Error{Op: "Get", URL: "http://h/x?token=abc", Err: errors.New("bad s3cret-value")}
	msg := transportMessage(CodeConnectionFailed, err, r, time.Second, 10)
	if strings.Contains(msg, "s3cret") || strings.Contains(msg, "token=abc") {
		t.Fatalf("message leaks: %s", msg)
	}
	if msg := transportMessage(CodeTimeout, err, r, 1500*time.Millisecond, 10); msg != "request did not complete within the 1.5s timeout" {
		t.Fatalf("timeout message: %s", msg)
	}
	if msg := transportMessage(CodeResponseTooLarge, err, r, 0, 2048); !strings.Contains(msg, "2048") {
		t.Fatalf("size message: %s", msg)
	}
}
