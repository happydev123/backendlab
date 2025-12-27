package mock

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/testutil"
)

func TestHandlerRoutes(t *testing.T) {
	h := testServer().Handler()
	tests := []struct {
		method, path string
		want         int
	}{
		{"GET", "/baseline/health", 200},
		{"GET", "/candidate/health", 200},
		{"HEAD", "/baseline/health", 200},
		{"GET", "/baseline/invoices", 200},
		{"GET", "/candidate/invoices/inv-1003", 200},
		{"GET", "/candidate/invoices/inv-0000", 404},
		{"GET", "/baseline/slow?delay=1ms", 200},
		{"GET", "/baseline/slow?delay=bogus", 400},
		{"GET", "/baseline/stream/chat", 200},
		{"GET", "/candidate/stream/gap?gap=1ms", 200},
		{"GET", "/candidate/stream/truncated", 200},
		{"POST", "/baseline/counter/increment", 200},
		{"GET", "/baseline/counter", 200},
		{"POST", "/_mock/reset", 200},
		{"GET", "/_mock/config", 200},
		{"POST", "/baseline/health", 405},
		{"GET", "/baseline/counter/increment", 405},
		{"GET", "/_mock/reset", 405},
		{"GET", "/staging/health", 404},
		{"GET", "/health", 404},
		{"GET", "/", 404},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestHandlerVariantsShareCountersPerVariant(t *testing.T) {
	s := testServer()
	h := s.Handler()
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/candidate/counter/increment", nil))
	}
	if s.Count(Candidate) != 2 || s.Count(Baseline) != 0 {
		t.Fatalf("counts baseline=%d candidate=%d", s.Count(Baseline), s.Count(Candidate))
	}
}

func TestStreamOverHTTP(t *testing.T) {
	srv := testutil.NewServer(t, testServer().Handler())
	client := &http.Client{Transport: testutil.Transport(), Timeout: 10 * time.Second}
	for _, v := range Variants() {
		resp, err := client.Get(srv.URL + "/" + string(v) + "/stream/chat")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, chatPayload(v)) {
			t.Fatalf("%s: received %q, want %q", v, body, chatPayload(v))
		}
		if resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Errorf("%s: Content-Type %q", v, resp.Header.Get("Content-Type"))
		}
	}
}

// readChunks sends a raw HTTP/1.1 request and returns the body as the
// sequence of transfer chunks the server wrote, so tests can see the
// framing that a normal client hides.
func readChunks(t *testing.T, addr, path string) [][]byte {
	t.Helper()
	dial := testutil.DialContext()
	if dial == nil {
		d := &net.Dialer{Timeout: 5 * time.Second}
		dial = d.DialContext
	}
	conn, err := dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: mock\r\nConnection: close\r\n\r\n", path)

	tp := textproto.NewReader(bufio.NewReader(conn))
	status, err := tp.ReadLine()
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 200") {
		t.Fatalf("status line %q: %v", status, err)
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Transfer-Encoding") != "chunked" {
		t.Fatalf("Transfer-Encoding %q", hdr.Get("Transfer-Encoding"))
	}
	var chunks [][]byte
	for {
		line, err := tp.ReadLine()
		if err != nil {
			t.Fatalf("chunk size: %v", err)
		}
		n, err := strconv.ParseInt(line, 16, 32)
		if err != nil {
			t.Fatalf("chunk size %q: %v", line, err)
		}
		if n == 0 {
			return chunks
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(tp.R, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf[n:]) != "\r\n" {
			t.Fatalf("chunk not terminated by CRLF: %q", buf[n:])
		}
		chunks = append(chunks, buf[:n])
	}
}

func TestStreamFragmentationOnTheWire(t *testing.T) {
	srv := testutil.NewServer(t, testServer().Handler())
	addr := srv.Listener.Addr().String()

	chunks := readChunks(t, addr, "/candidate/stream/chat")
	want := fragments(chatPayload(Candidate))
	if len(chunks) != len(want) {
		t.Fatalf("%d chunks on the wire, want %d:\n%q", len(chunks), len(want), chunks)
	}
	for i := range want {
		if !bytes.Equal(chunks[i], want[i]) {
			t.Fatalf("chunk %d = %q, want %q", i, chunks[i], want[i])
		}
	}
	k := classify(chunks)
	if !k.crlf || !k.multibyte || !k.midLine {
		t.Fatalf("wire framing lacks awkward boundaries: %+v", k)
	}

	trunc := bytes.Join(readChunks(t, addr, "/baseline/stream/truncated"), nil)
	if !bytes.Equal(trunc, truncatedPayload()) {
		t.Fatalf("truncated stream body %q", trunc)
	}
}
