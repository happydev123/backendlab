package dashboard

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/web"
)

// Dashboard serves the HTML pages.
type Dashboard struct {
	api    *httpapi.API
	tmpl   *renderer
	static http.Handler
	log    *slog.Logger
}

// New parses the embedded templates and returns a Dashboard backed by api.
func New(api *httpapi.API) (*Dashboard, error) {
	if api == nil {
		return nil, errors.New("dashboard: api is required")
	}
	tmpl, err := parseTemplates(web.Templates)
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		return nil, err
	}
	return &Dashboard{api: api, tmpl: tmpl, static: staticHandler(static), log: api.Logger()}, nil
}

// staticHandler serves the embedded assets under /static/. Directory
// listings are not served. Assets are not content-hashed, so they may be
// cached only briefly.
func staticHandler(fsys fs.FS) http.Handler {
	files := http.StripPrefix("/static/", http.FileServer(http.FS(fsys)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		files.ServeHTTP(w, r)
	})
}

// render executes page name with content and writes it with status.
// Output is buffered so a template error yields a clean 500.
func (d *Dashboard) render(w http.ResponseWriter, r *http.Request, status int, name, title, active string, content any) {
	d.renderPage(w, r, status, name, page{Title: title, Active: active, Content: content})
}

func (d *Dashboard) renderPage(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	p.RequestID = httpapi.RequestIDFrom(r.Context())
	if sess, ok := httpapi.SessionFrom(r.Context()); ok {
		p.LoggedIn, p.CSRFToken = true, sess.CSRFToken
	}
	var buf bytes.Buffer
	if err := d.tmpl.render(&buf, name, p); err != nil {
		httpapi.RequestLogger(r.Context(), d.log).Error("render page", slog.String("page", name), slog.String("error", err.Error()))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes()) //nolint:errcheck // client went away
}

var errorHeadings = map[int]string{
	http.StatusBadRequest:            "Invalid request",
	http.StatusUnauthorized:          "Sign-in required",
	http.StatusForbidden:             "Not permitted",
	http.StatusNotFound:              "Not found",
	http.StatusConflict:              "Not possible in the current state",
	http.StatusRequestEntityTooLarge: "Request too large",
	http.StatusUnsupportedMediaType:  "Unsupported request",
	http.StatusTooManyRequests:       "Too many attempts",
	http.StatusServiceUnavailable:    "Temporarily unavailable",
}

// renderError shows e on the error page with e's status.
func (d *Dashboard) renderError(w http.ResponseWriter, r *http.Request, e *httpapi.Error) {
	heading := errorHeadings[e.Status]
	if heading == "" {
		heading = "Something went wrong"
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(e.RetryAfter/time.Second)))
	}
	d.render(w, r, e.Status, "error", heading, "", errorView{Heading: heading, Message: e.Message})
}

// requireLogin redirects requests without a session to the sign-in page.
func (d *Dashboard) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httpapi.SessionFrom(r.Context()); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCSRF parses a URL-encoded form (the body must already be bounded
// by httpapi.LimitBody) and rejects it unless csrf_token matches the
// session. Multipart forms check the token in their handler instead.
func (d *Dashboard) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				d.renderError(w, r, httpapi.Errorf(http.StatusRequestEntityTooLarge, httpapi.CodeTooLarge, "the form exceeds %d bytes", tooBig.Limit))
				return
			}
			d.renderError(w, r, httpapi.Errorf(http.StatusBadRequest, httpapi.CodeInvalidRequest, "the form could not be read"))
			return
		}
		if !httpapi.CSRFValid(r, r.PostForm.Get(httpapi.CSRFField)) {
			d.renderError(w, r, httpapi.Errorf(http.StatusForbidden, httpapi.CodeCSRF,
				"the form's CSRF token is missing or invalid; reload the page and try again"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// notFound renders the 404 page for unknown dashboard paths.
func (d *Dashboard) notFound(w http.ResponseWriter, r *http.Request) {
	d.renderError(w, r, httpapi.Errorf(http.StatusNotFound, httpapi.CodeNotFound, "the page does not exist"))
}

// limitBody bounds the request body like httpapi.LimitBody but answers an
// oversized declared length with the HTML error page.
func (d *Dashboard) limitBody(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				d.renderError(w, r, httpapi.Errorf(http.StatusRequestEntityTooLarge, httpapi.CodeTooLarge, "the request exceeds %d bytes", max))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}
