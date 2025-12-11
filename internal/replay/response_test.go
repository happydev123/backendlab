package replay

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/redact"
)

func TestReadBody(t *testing.T) {
	data, err := readBody(strings.NewReader("12345"), 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("exact limit: %q %v", data, err)
	}
	if _, err := readBody(strings.NewReader("123456"), 5); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("over limit: %v", err)
	}
}

type countingCloser struct {
	io.Reader
	read   int64
	closed bool
}

func (c *countingCloser) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *countingCloser) Close() error { c.closed = true; return nil }

func TestCloseBodyDrainsBounded(t *testing.T) {
	body := &countingCloser{Reader: strings.NewReader(strings.Repeat("x", drainLimit*3))}
	closeBody(body)
	if !body.closed || body.read != drainLimit {
		t.Fatalf("closed=%v read=%d", body.closed, body.read)
	}
}

func TestLimitedReader(t *testing.T) {
	got, err := io.ReadAll(newLimitedReader(strings.NewReader("abcdef"), 6))
	if err != nil || string(got) != "abcdef" {
		t.Fatalf("within limit: %q %v", got, err)
	}
	got, err = io.ReadAll(newLimitedReader(strings.NewReader("abcdefgh"), 6))
	if !errors.Is(err, errResponseTooLarge) || string(got) != "abcdef" {
		t.Fatalf("over limit: %q %v", got, err)
	}
	r := newLimitedReader(strings.NewReader("abc"), 0)
	if _, err := r.Read(make([]byte, 4)); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("zero limit: %v", err)
	}
	if _, err := r.Read(make([]byte, 4)); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("error must persist: %v", err)
	}
}

func TestRetainedBody(t *testing.T) {
	r := redact.Default().WithSecrets("topsecret")
	decoded := map[string]any{"password": "p", "note": "has topsecret"}
	got := retainedBody(nil, decoded, true, r, 1000)
	if strings.Contains(got, `"p"`) || strings.Contains(got, "topsecret") {
		t.Fatalf("json body not redacted: %s", got)
	}
	got = retainedBody([]byte("plain topsecret text that is long"), nil, false, r, 12)
	if strings.Contains(got, "topsecret") || len(got) > 15 {
		t.Fatalf("text body: %q", got)
	}
}
