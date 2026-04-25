package egress

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
)

func TestNormalizeHost(t *testing.T) {
	ok := map[string]string{
		"API.Example.COM":       "api.example.com",
		"api.example.com.":      "api.example.com",
		"svc_1.internal":        "svc_1.internal",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"localhost":             "localhost",
		"1.example":             "1.example",
	}
	for in, want := range ok {
		got, err := normalizeHost(in)
		if err != nil || got != want {
			t.Errorf("normalizeHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", ".", "a..b", "api.example.com..", "bücher.example", "exa mple.com",
		"evil.com%2f", "2130706433", "127.1", "0x7f.1", "10.0.0.010", "example.0x10",
		strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "com",
	} {
		if got, err := normalizeHost(in); err == nil {
			t.Errorf("normalizeHost(%q) = %q, want error", in, got)
		}
	}
}

func TestCheckURL(t *testing.T) {
	open := config.Egress{}
	listed := config.Egress{AllowHosts: []string{
		"api.example.com",
		"*.svc.example.net",
		"reports.example.org:8443",
		"203.0.113.10:8080", // documentation range: listed, but CheckIP still refuses it
		"93.184.216.34",
		"[2606:4700:4700::1111]:443",
	}}
	loop := config.Egress{AllowLoopback: true}
	tests := []struct {
		name  string
		cfg   config.Egress
		url   string
		allow bool
	}{
		{"open public name", open, "https://anything.example.com/x", true},
		{"open public ip", open, "http://93.184.216.34/", true},
		{"open loopback ip", open, "http://127.0.0.1:8080/", false},
		{"open mapped loopback", open, "http://[::ffff:127.0.0.1]/", false},
		{"open unspecified", open, "http://0.0.0.0/", false},
		{"open v6 unspecified", open, "http://[::]/", false},
		{"open metadata", open, "http://169.254.169.254/latest/meta-data/", false},
		{"open cgnat", open, "http://100.64.1.1/", false},
		{"open ula", open, "http://[fd00::1]/", false},
		{"open zoned", open, "http://[fe80::1%25eth0]/", false},
		{"open decimal ip", open, "http://2130706433/", false},
		{"open short ip", open, "http://127.1/", false},
		{"loopback allowed", loop, "http://127.0.0.1:8090/baseline", true},
		{"loopback name is not checked here", loop, "http://localhost:8090/", true},

		{"scheme ftp", open, "ftp://example.com/", false},
		{"scheme file", open, "file:///etc/passwd", false},
		{"scheme upper", open, "HTTPS://api.example.com/", true},
		{"userinfo", open, "https://user:pass@api.example.com/", false},
		{"userinfo empty password", open, "https://user@api.example.com/", false},
		{"no host", open, "http:///path", false},
		{"opaque", open, "http:api.example.com", false},
		{"bad port", open, "http://api.example.com:0/", false},

		{"exact", listed, "https://api.example.com/v1", true},
		{"exact upper", listed, "https://API.EXAMPLE.COM/v1", true},
		{"exact trailing dot", listed, "https://api.example.com./v1", true},
		{"exact explicit default port", listed, "https://api.example.com:443/", true},
		{"exact any port", listed, "http://api.example.com:9000/", true},
		{"suffix trick", listed, "https://api.example.com.evil.net/", false},
		{"prefix trick", listed, "https://evilapi.example.com/", false},
		{"wildcard one label", listed, "https://a.svc.example.net/", true},
		{"wildcard two labels", listed, "https://a.b.svc.example.net/", true},
		{"wildcard bare suffix", listed, "https://svc.example.net/", false},
		{"wildcard lookalike", listed, "https://evilsvc.example.net/", false},
		{"port pinned ok", listed, "https://reports.example.org:8443/", true},
		{"port pinned default", listed, "https://reports.example.org/", false},
		{"port pinned other", listed, "https://reports.example.org:9443/", false},
		{"ip listed", listed, "http://93.184.216.34/", true},
		{"ip listed v6", listed, "https://[2606:4700:4700::1111]/", true},
		{"ip listed v6 other port", listed, "http://[2606:4700:4700::1111]/", false},
		{"ip listed but denied by range", listed, "http://203.0.113.10:8080/", false},
		{"ip not listed", listed, "http://8.8.8.8/", false},
		{"unlisted name", listed, "https://example.org/", false},
		{"name not matched by ip entry", listed, "http://93.184.216.34.nip.io/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.url)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.url, err)
			}
			err = mustPolicy(t, tt.cfg).CheckURL(u)
			if tt.allow {
				if err != nil {
					t.Fatalf("CheckURL(%s) = %v, want allowed", tt.url, err)
				}
				return
			}
			if !errors.Is(err, ErrDestinationDenied) {
				t.Fatalf("CheckURL(%s) = %v, want ErrDestinationDenied", tt.url, err)
			}
		})
	}
}

func TestCheckURLErrorOmitsSecrets(t *testing.T) {
	p := mustPolicy(t, config.Egress{AllowHosts: []string{"api.example.com"}})
	for _, raw := range []string{
		"https://alice:hunter2@api.example.com/path?token=s3cret",
		"https://other.example.com/private/path?token=s3cret",
		"http://10.0.0.1/admin?token=s3cret",
	} {
		u, _ := url.Parse(raw)
		err := p.CheckURL(u)
		if err == nil {
			t.Fatalf("CheckURL(%s) allowed", raw)
		}
		for _, secret := range []string{"hunter2", "s3cret", "/path", "/private", "/admin", "alice"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q leaks %q", err, secret)
			}
		}
	}
}

func TestCheckRedirect(t *testing.T) {
	p := mustPolicy(t, config.Egress{AllowHosts: []string{"api.example.com"}})
	ok, _ := http.NewRequest(http.MethodGet, "https://api.example.com/next", nil)
	if err := p.CheckRedirect(ok); err != nil {
		t.Fatalf("allowed redirect refused: %v", err)
	}
	for _, target := range []string{"http://169.254.169.254/", "https://elsewhere.example/", "http://127.0.0.1/"} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if err := p.CheckRedirect(req); !errors.Is(err, ErrDestinationDenied) {
			t.Errorf("redirect to %s = %v, want denied", target, err)
		}
	}
	if err := p.CheckRedirect(nil); !errors.Is(err, ErrDestinationDenied) {
		t.Errorf("nil request = %v", err)
	}
	if err := p.CheckURL(nil); !errors.Is(err, ErrDestinationDenied) {
		t.Errorf("nil URL = %v", err)
	}
}

func TestParseHostPattern(t *testing.T) {
	tests := []struct {
		in   string
		want hostPattern
	}{
		{"API.example.com", hostPattern{host: "api.example.com"}},
		{"*.Example.com.", hostPattern{host: "example.com", wildcard: true}},
		{"api.example.com:8443", hostPattern{host: "api.example.com", port: "8443"}},
		{"*.example.com:443", hostPattern{host: "example.com", wildcard: true, port: "443"}},
	}
	for _, tt := range tests {
		got, err := parseHostPattern(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("parseHostPattern(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"[::1]", "::1", "[2001:db8::1]:443", "10.0.0.1", "10.0.0.1:80"} {
		got, err := parseHostPattern(in)
		if err != nil || !got.ip.IsValid() {
			t.Errorf("parseHostPattern(%q) = %+v, %v; want an IP entry", in, got, err)
		}
	}
}
