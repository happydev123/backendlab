package suite

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/limits"
)

var (
	caseIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	pathParamRef    = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	headerNameToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// forbiddenHeaders may not be set by suites: they are controlled by the
// HTTP client or by target configuration.
var forbiddenHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true,
	"connection": true, "upgrade": true, "te": true, "trailer": true,
}

// credentialHeaders are supplied only by target configuration so that
// secrets never live in suite files.
var credentialHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
}

var jsonTypes = map[string]bool{
	"object": true, "array": true, "string": true, "number": true, "boolean": true, "null": true,
}

// MaxTextBody bounds an inline text body.
const MaxTextBody = 64 << 10

// ValidationError collects every problem found in a suite.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid suite: " + e.Problems[0]
	}
	return fmt.Sprintf("invalid suite (%d problems):\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

type problems struct{ list []string }

func (p *problems) add(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

// Validate checks a decoded suite against the format rules and lim.
func Validate(s *Suite, lim limits.Limits) error {
	var p problems
	if s.Version != CurrentVersion {
		p.add("version must be %d (got %d)", CurrentVersion, s.Version)
	}
	if strings.TrimSpace(s.Name) == "" {
		p.add("name is required")
	}
	if len(s.Cases) == 0 {
		p.add("at least one case is required")
	}
	if len(s.Cases) > lim.MaxCases {
		p.add("suite has %d cases; the limit is %d", len(s.Cases), lim.MaxCases)
	}
	if s.Defaults.Timeout.Std() > lim.MaxRequestTime {
		p.add("defaults.timeout exceeds the maximum request timeout %s", lim.MaxRequestTime)
	}
	validateHeaders(&p, "defaults.headers", s.Defaults.Headers, false)
	validateCompare(&p, "defaults.compare", &s.Defaults.Compare)

	seen := map[string]bool{}
	captured := map[string]string{}
	for i := range s.Cases {
		c := &s.Cases[i]
		where := fmt.Sprintf("cases[%d]", i)
		if c.ID != "" {
			where = fmt.Sprintf("case %q", c.ID)
		}
		switch {
		case c.ID == "":
			p.add("%s: id is required", where)
		case !caseIDPattern.MatchString(c.ID):
			p.add("%s: id must match %s", where, caseIDPattern)
		case seen[c.ID]:
			p.add("%s: duplicate id", where)
		}
		seen[c.ID] = true
		validateCase(&p, where, c, lim)

		refs, err := c.Refs()
		if err != nil {
			p.add("%s: %v", where, err)
		}
		for _, name := range sortedKeys(refs) {
			if _, ok := captured[name]; !ok {
				p.add("%s: %s references ${%s}, which no earlier case captures", where, strings.Join(refs[name], ", "), name)
			}
		}
		for _, cp := range c.Capture {
			if !varName.MatchString(cp.Name) {
				p.add("%s: capture name %q is invalid", where, cp.Name)
				continue
			}
			if prev, dup := captured[cp.Name]; dup {
				p.add("%s: capture %q is already defined by case %q", where, cp.Name, prev)
				continue
			}
			if _, err := jsonptr.Parse(cp.Pointer); err != nil {
				p.add("%s: capture %q: %v", where, cp.Name, err)
			}
			captured[cp.Name] = c.ID
		}
	}
	if len(p.list) > 0 {
		return &ValidationError{Problems: p.list}
	}
	return nil
}

func validateCase(p *problems, where string, c *Case, lim limits.Limits) {
	method := strings.ToUpper(c.Method)
	if c.Method != method {
		p.add("%s: method must be upper case", where)
	}
	if !AllowedMethods[method] {
		p.add("%s: method %q is not supported", where, c.Method)
	}
	validatePath(p, where, c)
	for k := range c.Query {
		if k == "" {
			p.add("%s: empty query parameter name", where)
		}
	}
	validateHeaders(p, where+": headers", c.Headers, true)
	if c.Timeout.Std() > lim.MaxRequestTime {
		p.add("%s: timeout exceeds the maximum request timeout %s", where, lim.MaxRequestTime)
	}
	if c.Body != nil {
		validateBody(p, where, method, c.Body, lim)
	}
	validateExpect(p, where, &c.Expect)
	validateCompare(p, where+": compare", &c.Compare)
	if c.Stream != nil {
		validateStream(p, where, c.Stream)
		if len(c.Capture) > 0 {
			p.add("%s: captures are not supported on stream cases", where)
		}
	}
}

func validatePath(p *problems, where string, c *Case) {
	path := c.Path
	switch {
	case path == "":
		p.add("%s: path is required", where)
		return
	case !strings.HasPrefix(path, "/"):
		p.add("%s: path must be relative to the target and start with '/'", where)
	case strings.HasPrefix(path, "//"):
		p.add("%s: path must not start with '//' (it would name a host)", where)
	case strings.Contains(path, "://"):
		p.add("%s: path must not contain a scheme or host", where)
	}
	if strings.ContainsAny(path, "?#") {
		p.add("%s: path must not contain '?' or '#'; use query instead", where)
	}
	if strings.ContainsAny(path, " \t\r\n\\") {
		p.add("%s: path contains whitespace or backslashes", where)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." || seg == "." {
			p.add("%s: path must not contain dot segments", where)
			break
		}
	}
	if strings.Contains(path, "$") {
		p.add("%s: variables are not expanded in path; use {name} with path_params", where)
	}
	used := map[string]bool{}
	for _, m := range pathParamRef.FindAllStringSubmatch(path, -1) {
		used[m[1]] = true
		if _, ok := c.PathParams[m[1]]; !ok {
			p.add("%s: path placeholder {%s} has no path_params entry", where, m[1])
		}
	}
	stripped := pathParamRef.ReplaceAllString(path, "")
	if strings.ContainsAny(stripped, "{}") {
		p.add("%s: path has an invalid placeholder", where)
	}
	for _, k := range sortedKeys(c.PathParams) {
		if !used[k] {
			p.add("%s: path_params.%s is not used in path", where, k)
		}
	}
}

func validateHeaders(p *problems, where string, h map[string]string, allowRefs bool) {
	for _, name := range sortedKeys(h) {
		lower := strings.ToLower(name)
		switch {
		case !headerNameToken.MatchString(name):
			p.add("%s: invalid header name %q", where, name)
		case forbiddenHeaders[lower]:
			p.add("%s: header %s is controlled by the client and cannot be set", where, name)
		case credentialHeaders[lower]:
			p.add("%s: header %s must come from target configuration, not the suite", where, name)
		}
		v := h[name]
		if strings.ContainsAny(v, "\r\n\x00") {
			p.add("%s: header %s contains control characters", where, name)
		}
		if !allowRefs && strings.Contains(v, "${") {
			p.add("%s: header %s: variables are not allowed in defaults", where, name)
		}
	}
}

func validateBody(p *problems, where, method string, b *Body, lim limits.Limits) {
	if method == "GET" || method == "HEAD" {
		p.add("%s: %s requests must not have a body", where, method)
	}
	switch {
	case b.JSON != nil && b.Text != nil:
		p.add("%s: body must set exactly one of json or text", where)
	case b.JSON == nil && b.Text == nil:
		p.add("%s: body must set json or text", where)
	case b.Text != nil:
		if len(*b.Text) > MaxTextBody || int64(len(*b.Text)) > lim.MaxRequestBytes {
			p.add("%s: text body exceeds the inline body limit", where)
		}
	}
	if strings.ContainsAny(b.ContentType, "\r\n") {
		p.add("%s: body.content_type contains control characters", where)
	}
}

func validateExpect(p *problems, where string, e *Expect) {
	for _, code := range e.Status {
		if code < 100 || code > 599 {
			p.add("%s: expect.status %d is not a valid HTTP status", where, code)
		}
	}
	for _, name := range sortedKeys(e.Headers) {
		if !headerNameToken.MatchString(name) {
			p.add("%s: expect.headers: invalid header name %q", where, name)
		}
	}
	for i, a := range e.JSON {
		at := fmt.Sprintf("%s: expect.json[%d]", where, i)
		if _, err := jsonptr.Parse(a.Pointer); err != nil {
			p.add("%s: %v", at, err)
		}
		ops := 0
		if a.Equals != nil {
			ops++
		}
		if a.Exists != nil {
			ops++
		}
		if a.Type != "" {
			ops++
			if !jsonTypes[a.Type] {
				p.add("%s: type %q is not a JSON type", at, a.Type)
			}
		}
		if a.Matches != "" {
			ops++
			if _, err := regexp.Compile(a.Matches); err != nil {
				p.add("%s: matches is not a valid regular expression: %v", at, err)
			}
		}
		if ops != 1 {
			p.add("%s: set exactly one of equals, exists, type or matches", at)
		}
	}
	if e.Schema != "" && strings.TrimSpace(e.Schema) != e.Schema {
		p.add("%s: expect.schema has surrounding whitespace", where)
	}
}

func validateCompare(p *problems, where string, c *CompareSpec) {
	for _, pat := range c.Ignore {
		if _, err := jsonptr.ParsePattern(pat); err != nil {
			p.add("%s: ignore: %v", where, err)
		} else if pat == "" {
			p.add("%s: ignore must not exclude the whole document", where)
		}
	}
	for _, h := range c.Headers {
		if !headerNameToken.MatchString(h) {
			p.add("%s: headers: invalid header name %q", where, h)
		}
	}
	if c.Tolerance != nil {
		validateTolerance(p, where+": tolerance", *c.Tolerance)
	}
	for i, pt := range c.PathTolerances {
		at := fmt.Sprintf("%s: path_tolerances[%d]", where, i)
		if _, err := jsonptr.ParsePattern(pt.Pointer); err != nil {
			p.add("%s: %v", at, err)
		}
		validateTolerance(p, at, pt.Tolerance)
	}
	for i, u := range c.Unordered {
		at := fmt.Sprintf("%s: unordered[%d]", where, i)
		if _, err := jsonptr.ParsePattern(u.Pointer); err != nil {
			p.add("%s: %v", at, err)
		}
		if strings.TrimSpace(u.Key) == "" {
			p.add("%s: key is required; unordered comparison needs a matching key", at)
		}
	}
}

func validateTolerance(p *problems, where string, t Tolerance) {
	if t.Absolute == "" && t.Relative == "" {
		p.add("%s: set absolute or relative", where)
	}
	for _, f := range []struct{ name, v string }{{"absolute", t.Absolute}, {"relative", t.Relative}} {
		name, v := f.name, f.v
		if v == "" {
			continue
		}
		d, err := decimal.NewFromString(v)
		if err != nil {
			p.add("%s: %s %q is not a decimal number", where, name, v)
			continue
		}
		if d.IsNegative() {
			p.add("%s: %s must not be negative", where, name)
		}
	}
}

var knownAdapters = map[string]bool{"": true, "chat-delta": true}

func validateStream(p *problems, where string, s *StreamSpec) {
	for _, ev := range s.RequiredEvents {
		if ev == "" || strings.ContainsAny(ev, "\r\n") {
			p.add("%s: stream.required_events contains an invalid event type", where)
		}
	}
	if s.RequireTerminal && s.TerminalEvent == "" && s.TerminalData == "" {
		p.add("%s: stream.require_terminal needs terminal_event or terminal_data", where)
	}
	if s.Schema != "" && !s.JSONPayloads {
		p.add("%s: stream.schema requires json_payloads: true", where)
	}
	if !knownAdapters[s.Adapter] {
		p.add("%s: stream.adapter %q is not supported", where, s.Adapter)
	}
}

// IsValidationError reports whether err is a suite validation error.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// CanonicalHeader returns the canonical form of a header name.
func CanonicalHeader(name string) string { return http.CanonicalHeaderKey(name) }
