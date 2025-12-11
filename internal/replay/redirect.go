package replay

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
)

// maxRedirects is the most redirects followed for one request when the
// target enables following.
const maxRedirects = 5

// errTooManyRedirects stops a redirect chain longer than maxRedirects.
var errTooManyRedirects = fmt.Errorf("stopped after %d redirects", maxRedirects)

// redirectBlockedError wraps a refusal from the caller's redirect policy.
type redirectBlockedError struct{ err error }

func (e *redirectBlockedError) Error() string { return "redirect blocked: " + e.err.Error() }
func (e *redirectBlockedError) Unwrap() error { return e.err }

// redirectPolicy returns an http.Client CheckRedirect function for target.
//
// Without FollowRedirects the 3xx response itself is returned. With it, at
// most maxRedirects hops are followed. Once any hop leaves the origin
// (scheme, host and port) of the first request, every header supplied by
// the target, which includes all credentials, is removed from that hop and
// every later hop, even if the chain returns to the original origin. The
// extra policy, when set, is consulted for each hop after headers have
// been adjusted.
func redirectPolicy(target *config.ResolvedTarget, extra func(*http.Request) error) func(*http.Request, []*http.Request) error {
	if !target.FollowRedirects {
		return func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	credentialNames := make([]string, 0, len(target.Headers))
	for name := range target.Headers {
		credentialNames = append(credentialNames, name)
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errTooManyRedirects
		}
		if leftOrigin(req, via) {
			for _, name := range credentialNames {
				req.Header.Del(name)
			}
			// net/http may re-add these from the original request on
			// same-domain hops; they are never wanted after leaving.
			req.Header.Del("Authorization")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Cookie")
		}
		if extra != nil {
			if err := extra(req); err != nil {
				return &redirectBlockedError{err: err}
			}
		}
		return nil
	}
}

// leftOrigin reports whether req, or any earlier hop after the first
// request, has a different origin from the first request.
func leftOrigin(req *http.Request, via []*http.Request) bool {
	if len(via) == 0 {
		return false
	}
	first := origin(via[0].URL)
	for _, r := range via[1:] {
		if origin(r.URL) != first {
			return true
		}
	}
	return origin(req.URL) != first
}

// origin returns scheme://host:port with the scheme and host lower-cased
// and the default port made explicit.
func origin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
