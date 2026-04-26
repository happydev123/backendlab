package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/testutil"
)

// fakeResolver answers from a fixed table and counts lookups.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	calls   int
}

func (f *fakeResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	addrs, ok := f.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []netip.Addr
	for _, a := range addrs {
		if network == "ip4" && !a.Unmap().Is4() || network == "ip6" && a.Unmap().Is4() {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

// dialRecorder is a net.Dialer whose Control hook records every address a
// connection attempt is made to and then aborts the attempt, so no packet
// leaves the host.
type dialRecorder struct {
	mu        sync.Mutex
	attempted []string
}

func (r *dialRecorder) dialer() *net.Dialer {
	return &net.Dialer{
		Timeout: 2 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.attempted = append(r.attempted, address)
			return errors.New("dial blocked by test")
		},
	}
}

func (r *dialRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.attempted...)
}

func policyWithResolver(t *testing.T, cfg config.Egress, res *fakeResolver) *Policy {
	t.Helper()
	p := mustPolicy(t, cfg)
	p.resolver = res
	return p
}

func TestDialDeniedAddressIsNeverDialed(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{
		"internal.example.com": addrs("10.0.0.5"),
		"mixed.example.com":    addrs("93.184.216.34", "169.254.169.254"),
		"mapped.example.com":   addrs("::ffff:127.0.0.1"),
		"ula.example.com":      addrs("2606:4700::1", "fd00::1"),
	}}
	p := policyWithResolver(t, config.Egress{}, res)
	for host, wantAddr := range map[string]string{
		"internal.example.com": "10.0.0.5",
		"mixed.example.com":    "169.254.169.254",
		"mapped.example.com":   "127.0.0.1",
		"ula.example.com":      "fd00::1",
	} {
		t.Run(host, func(t *testing.T) {
			rec := &dialRecorder{}
			_, err := p.DialContext(rec.dialer())(context.Background(), "tcp", host+":443")
			var de *DeniedError
			if !errors.As(err, &de) || !errors.Is(err, ErrDestinationDenied) {
				t.Fatalf("dial = %v, want DeniedError", err)
			}
			if de.Host != host || de.Addr != netip.MustParseAddr(wantAddr) {
				t.Errorf("DeniedError host=%q addr=%s, want %q %s", de.Host, de.Addr, host, wantAddr)
			}
			if got := rec.list(); len(got) != 0 {
				t.Fatalf("denied destination was dialed: %v", got)
			}
		})
	}
}

func TestDialConnectsToCheckedLiteral(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{
		"api.example.com": addrs("93.184.216.34", "::ffff:93.184.216.35", "93.184.216.34"),
	}}
	p := policyWithResolver(t, config.Egress{AllowHosts: []string{"api.example.com"}}, res)
	rec := &dialRecorder{}
	_, err := p.DialContext(rec.dialer())(context.Background(), "tcp", "API.example.com.:443")
	if err == nil || errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("dial = %v, want the test's blocked-dial error", err)
	}
	// Each distinct checked address is tried once, unmapped, as a literal.
	got := strings.Join(rec.list(), ",")
	if got != "93.184.216.34:443,93.184.216.35:443" {
		t.Fatalf("dialed %s", got)
	}
	if res.calls != 1 {
		t.Fatalf("resolver called %d times, want exactly 1", res.calls)
	}
}

func TestDialHostAllowlistBeforeResolution(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"other.example.com": addrs("93.184.216.34")}}
	p := policyWithResolver(t, config.Egress{AllowHosts: []string{"api.example.com"}}, res)
	rec := &dialRecorder{}
	_, err := p.DialContext(rec.dialer())(context.Background(), "tcp", "other.example.com:443")
	if !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("dial = %v", err)
	}
	if res.calls != 0 || len(rec.list()) != 0 {
		t.Fatalf("resolver calls %d, dials %v; want none", res.calls, rec.list())
	}
}

func TestDialRejectsBadInput(t *testing.T) {
	p := policyWithResolver(t, config.Egress{}, &fakeResolver{})
	rec := &dialRecorder{}
	dial := p.DialContext(rec.dialer())
	for _, tc := range []struct{ network, addr string }{
		{"udp", "93.184.216.34:53"},
		{"unix", "/tmp/sock"},
		{"tcp", "no-port.example.com"},
		{"tcp", "api.example.com:http"},
		{"tcp", "api.example.com:0"},
		{"tcp", "127.0.0.1:80"},
		{"tcp", "[::]:80"},
		{"tcp", "2130706433:80"},
	} {
		if _, err := dial(context.Background(), tc.network, tc.addr); !errors.Is(err, ErrDestinationDenied) {
			t.Errorf("dial(%s, %s) = %v, want denied", tc.network, tc.addr, err)
		}
	}
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("dialed %v", got)
	}
}

func TestDialResolverErrors(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"v4only.example.com": addrs("93.184.216.34")}}
	p := policyWithResolver(t, config.Egress{}, res)
	rec := &dialRecorder{}
	dial := p.DialContext(rec.dialer())
	if _, err := dial(context.Background(), "tcp", "missing.example.com:443"); err == nil || errors.Is(err, ErrDestinationDenied) {
		t.Errorf("missing host: %v, want a resolution error", err)
	}
	if _, err := dial(context.Background(), "tcp6", "v4only.example.com:443"); err == nil || !strings.Contains(err.Error(), "no addresses") {
		t.Errorf("tcp6 to v4-only host: %v", err)
	}
	if len(rec.list()) != 0 {
		t.Fatalf("dialed %v", rec.list())
	}
}

func TestDialLoopbackAllowedEndToEnd(t *testing.T) {
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "reached "+r.Host)
	}))
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	res := &fakeResolver{answers: map[string][]netip.Addr{"mock.test": addrs("127.0.0.1")}}

	get := func(p *Policy) (string, error) {
		tr := testutil.Transport()
		tr.DisableKeepAlives = true
		tr.DialContext = p.DialContext(&net.Dialer{Timeout: 5 * time.Second})
		client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
		resp, err := client.Get("http://mock.test:" + port + "/")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return string(body), err
	}

	allowed := policyWithResolver(t, config.Egress{AllowLoopback: true}, res)
	body, err := get(allowed)
	if err != nil {
		t.Fatalf("loopback with allow_loopback: %v", err)
	}
	if body != "reached mock.test:"+port {
		t.Fatalf("body %q", body)
	}

	strict := policyWithResolver(t, config.Egress{}, res)
	if _, err := get(strict); !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("loopback without allow_loopback: %v, want ErrDestinationDenied", err)
	}
}

func TestDialNilDialerAndCancelledContext(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"api.example.com": addrs("93.184.216.34")}}
	p := policyWithResolver(t, config.Egress{}, res)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.DialContext(nil)(ctx, "tcp", "api.example.com:443"); !errors.Is(err, context.Canceled) {
		t.Fatalf("dial with cancelled context = %v", err)
	}
}
