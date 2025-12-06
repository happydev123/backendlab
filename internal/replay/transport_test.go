package replay

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/testutil"
)

// newTLSServer starts a TLS test server using the socket workaround.
func newTLSServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = testutil.WrapListener(srv.Listener)
	srv.StartTLS()
	t.Cleanup(func() {
		if testutil.AsyncClose() {
			go srv.Close()
			return
		}
		srv.Close()
	})
	return srv
}

func writeCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewTransportTLS(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	lim := limits.Default()

	get := func(tr *http.Transport) error {
		if d := testutil.DialContext(); d != nil {
			tr.DialContext = d
		}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		return err
	}

	tr, err := NewTransport(lim, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unsafe TLS config: %+v", tr.TLSClientConfig)
	}
	if tr.Proxy != nil {
		t.Fatal("proxy from environment must not be used")
	}
	if tr.MaxIdleConnsPerHost != lim.MaxConcurrency {
		t.Fatalf("idle conns per host = %d", tr.MaxIdleConnsPerHost)
	}
	if err := get(tr); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected certificate error without CA, got %v", err)
	}

	tr, err = NewTransport(lim, writeCA(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	if err := get(tr); err != nil {
		t.Fatalf("request with extra CA failed: %v", err)
	}
}

func TestNewTransportCAErrors(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.pem")
	if err := os.WriteFile(big, make([]byte, maxCAFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing": filepath.Join(dir, "nope.pem"),
		"no PEM":  empty,
		"too big": big,
	} {
		if _, err := NewTransport(limits.Default(), path); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
