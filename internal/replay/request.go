package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
	"github.com/happydev123/backendlab/internal/version"
)

// pathPlaceholder matches "{name}" in a case path.
var pathPlaceholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// userAgent identifies replay requests.
func userAgent() string { return version.Name + "/" + version.Version }

// buildRequest expands case c for target with the captured vars and
// returns a request bound to ctx. The returned error describes why the
// request could not be built; it never includes header values.
func buildRequest(ctx context.Context, target *config.ResolvedTarget, s *suite.Suite, c *suite.Case,
	vars map[string]string, lim limits.Limits) (*http.Request, error) {
	u, err := buildURL(target.BaseURL, c, vars)
	if err != nil {
		return nil, err
	}
	header, err := buildHeaders(target, s, c, vars)
	if err != nil {
		return nil, err
	}
	body, contentType, err := buildBody(c, vars, lim)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body) // also sets GetBody for 307/308 redirects
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(c.Method), u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header = header
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// buildURL joins the target base URL with the expanded case path and
// query. The base path is kept: base "http://h/v1" and path
// "/invoices/{id}" give "http://h/v1/invoices/<id>". Each path parameter
// is expanded and then escaped as a single segment, so a captured value
// such as "a/b c" becomes "a%2Fb%20c".
func buildURL(base *url.URL, c *suite.Case, vars map[string]string) (*url.URL, error) {
	var raw strings.Builder
	raw.WriteString(strings.TrimRight(base.EscapedPath(), "/"))
	last := 0
	for _, m := range pathPlaceholder.FindAllStringSubmatchIndex(c.Path, -1) {
		raw.WriteString(escapeLiteralPath(c.Path[last:m[0]]))
		name := c.Path[m[2]:m[3]]
		tpl, ok := c.PathParams[name]
		if !ok {
			return nil, fmt.Errorf("path placeholder {%s} has no path_params entry", name)
		}
		v, err := suite.ExpandString(tpl, vars)
		if err != nil {
			return nil, fmt.Errorf("path_params.%s: %w", name, err)
		}
		if v == "" || v == "." || v == ".." {
			return nil, fmt.Errorf("path_params.%s expands to an empty or dot segment", name)
		}
		raw.WriteString(url.PathEscape(v))
		last = m[1]
	}
	raw.WriteString(escapeLiteralPath(c.Path[last:]))
	rawPath := raw.String()
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}
	u := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: path, RawPath: rawPath}
	if len(c.Query) > 0 {
		q := url.Values{}
		keys := make([]string, 0, len(c.Query))
		for k := range c.Query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, err := suite.ExpandString(c.Query[k], vars)
			if err != nil {
				return nil, fmt.Errorf("query.%s: %w", k, err)
			}
			q.Set(k, v)
		}
		u.RawQuery = q.Encode() // Encode sorts by key
	}
	return u, nil
}

// escapeLiteralPath percent-encodes bytes of a literal path fragment that
// are not allowed in a URL path. Existing valid %XX escapes are kept.
func escapeLiteralPath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(ch)
		case pathByteAllowed(ch):
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func pathByteAllowed(ch byte) bool {
	switch {
	case 'a' <= ch && ch <= 'z', 'A' <= ch && ch <= 'Z', '0' <= ch && ch <= '9':
		return true
	}
	return strings.IndexByte("-._~!$&'()*+,;=:@/", ch) >= 0
}

func isHex(ch byte) bool {
	return ('0' <= ch && ch <= '9') || ('a' <= ch && ch <= 'f') || ('A' <= ch && ch <= 'F')
}

// buildHeaders combines target headers (which carry credentials) with the
// suite's effective headers. A suite header never replaces a header the
// target sets. Expanded values containing CR, LF or NUL are rejected.
func buildHeaders(target *config.ResolvedTarget, s *suite.Suite, c *suite.Case, vars map[string]string) (http.Header, error) {
	h := http.Header{}
	h.Set("User-Agent", userAgent())
	for name, vals := range target.Headers {
		h[http.CanonicalHeaderKey(name)] = append([]string(nil), vals...)
	}
	eff := s.EffectiveHeaders(c)
	names := make([]string, 0, len(eff))
	for name := range eff {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(target.Headers.Values(name)) > 0 {
			continue
		}
		v, err := suite.ExpandString(eff[name], vars)
		if err != nil {
			return nil, fmt.Errorf("headers.%s: %w", name, err)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return nil, fmt.Errorf("headers.%s: expanded value contains control characters", name)
		}
		h.Set(name, v)
	}
	return h, nil
}

// buildBody returns the encoded request body and its content type. JSON
// bodies have their string leaves expanded and are re-encoded; text bodies
// are sent literally.
func buildBody(c *suite.Case, vars map[string]string, lim limits.Limits) ([]byte, string, error) {
	if c.Body == nil {
		return nil, "", nil
	}
	var data []byte
	contentType := c.Body.ContentType
	switch {
	case c.Body.JSON != nil:
		v, err := suite.ExpandJSON(c.Body.JSON.V, vars)
		if err != nil {
			return nil, "", fmt.Errorf("body.json: %w", err)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return nil, "", fmt.Errorf("body.json: %w", err)
		}
		data = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		if contentType == "" {
			contentType = "application/json"
		}
	case c.Body.Text != nil:
		data = []byte(*c.Body.Text)
		if contentType == "" {
			contentType = "text/plain; charset=utf-8"
		}
	default:
		return nil, "", fmt.Errorf("body sets neither json nor text")
	}
	if int64(len(data)) > lim.MaxRequestBytes {
		return nil, "", fmt.Errorf("request body is %d bytes; the limit is %d", len(data), lim.MaxRequestBytes)
	}
	return data, contentType, nil
}
