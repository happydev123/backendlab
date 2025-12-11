package replay

import (
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/testutil"
)

// headerLog records selected request headers per path.
type headerLog struct {
	mu   sync.Mutex
	seen map[string]http.Header
}

func (l *headerLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]http.Header{}
	}
	l.seen[r.Host+r.URL.Path] = r.Header.Clone()
}

func (l *headerLog) get(key string) http.Header {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seen[key]
}

func TestRedirectPolicy(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	var log headerLog
	var loops atomic.Int32
	other := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		if r.URL.Path == "/back" {
			http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	otherURL, _ := url.Parse(other.URL)
	var self *url.URL
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/landing", http.StatusFound)
		case "/cross":
			http.Redirect(w, r, other.URL+"/landing", http.StatusTemporaryRedirect)
		case "/roundtrip":
			http.Redirect(w, r, other.URL+"/back?to="+url.QueryEscape(self.String()+"/final"), http.StatusFound)
		case "/loop":
			loops.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	self, _ = url.Parse(srv.URL)

	target := resolveTarget(t, "base", config.Target{
		BaseURL:         srv.URL,
		FollowRedirects: true,
		Headers: map[string]config.HeaderValue{
			"Authorization": {Env: "TOKEN", Prefix: "Bearer "},
			"X-Tenant":      {Value: "acme"},
		},
	}, map[string]string{"TOKEN": "secret-token"})

	do := func(tgt *config.ResolvedTarget, path string, extra func(*http.Request) error) (*http.Response, error) {
		client := &http.Client{Transport: testutil.Transport(), CheckRedirect: redirectPolicy(tgt, extra)}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header = tgt.Headers.Clone()
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		return resp, err
	}

	if _, err := do(target, "/same", nil); err != nil {
		t.Fatal(err)
	}
	if h := log.get(self.Host + "/landing"); h.Get("Authorization") != "Bearer secret-token" || h.Get("X-Tenant") != "acme" {
		t.Fatalf("same-origin redirect dropped headers: %v", h)
	}

	if _, err := do(target, "/cross", nil); err != nil {
		t.Fatal(err)
	}
	if h := log.get(otherURL.Host + "/landing"); h.Get("Authorization") != "" || h.Get("X-Tenant") != "" {
		t.Fatalf("cross-origin redirect kept target headers: %v", h)
	}

	if _, err := do(target, "/roundtrip", nil); err != nil {
		t.Fatal(err)
	}
	if h := log.get(self.Host + "/final"); h.Get("Authorization") != "" || h.Get("X-Tenant") != "" {
		t.Fatalf("headers restored after leaving the origin: %v", h)
	}

	_, err := do(target, "/loop", nil)
	if !errors.Is(err, errTooManyRedirects) {
		t.Fatalf("loop error = %v", err)
	}
	if n := loops.Load(); n != maxRedirects+1 {
		t.Fatalf("loop requests = %d, want %d", n, maxRedirects+1)
	}

	var hops []string
	policyErr := errors.New("egress denied")
	_, err = do(target, "/cross", func(r *http.Request) error {
		hops = append(hops, r.URL.Host)
		return policyErr
	})
	var blocked *redirectBlockedError
	if !errors.As(err, &blocked) || !errors.Is(err, policyErr) || len(hops) != 1 || hops[0] != otherURL.Host {
		t.Fatalf("extra policy: err=%v hops=%v", err, hops)
	}

	noFollow := *target
	noFollow.FollowRedirects = false
	resp, err := do(&noFollow, "/same", nil)
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect followed although disabled: %v %v", resp, err)
	}
}

func TestOrigin(t *testing.T) {
	cases := map[string]string{
		"http://Example.COM/a":     "http://example.com:80",
		"https://example.com:443/": "https://example.com:443",
		"https://example.com":      "https://example.com:443",
		"http://[::1]:8080/x":      "http://[::1]:8080",
	}
	for in, want := range cases {
		u, _ := url.Parse(in)
		if got := origin(u); got != want {
			t.Errorf("origin(%s) = %s, want %s", in, got, want)
		}
	}
}
