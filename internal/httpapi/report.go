package httpapi

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// ReportFile is a rendered report ready to download.
type ReportFile struct {
	Body        []byte
	ContentType string
	Filename    string
}

// reportFormats maps a format to its content type and file extension.
var reportFormats = map[report.Format]struct{ contentType, ext string }{
	report.FormatJSON:     {"application/json; charset=utf-8", "json"},
	report.FormatMarkdown: {"text/markdown; charset=utf-8", "md"},
	report.FormatText:     {"text/plain; charset=utf-8", "txt"},
}

// RenderReport renders the stored JSON report of run id in the format
// named by formatName (json, markdown or text; empty means json). The
// stored report was redacted and bounded by the worker; the text and
// Markdown renderers escape control characters and Markdown syntax in
// observed values, and color is always off.
func (a *API) RenderReport(ctx context.Context, id, formatName string) (*ReportFile, *Error) {
	format := report.FormatJSON
	if formatName != "" {
		f, err := report.ParseFormat(formatName)
		if err != nil {
			return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "format must be json, markdown or text")
		}
		format = f
	}
	run, e := a.LookupRun(ctx, id)
	if e != nil {
		return nil, e
	}
	data, err := a.store.GetReport(ctx, run.ID)
	switch {
	case errors.Is(err, store.ErrNoReport) && !run.Status.Terminal():
		return nil, Errorf(http.StatusConflict, CodeInvalidState, "run %s is %s; its report is available when it finishes", run.PublicID, run.Status)
	case errors.Is(err, store.ErrNoReport):
		return nil, Errorf(http.StatusNotFound, CodeNoReport, "run %s has no stored report", run.PublicID)
	case errors.Is(err, store.ErrNotFound):
		return nil, Errorf(http.StatusNotFound, CodeNotFound, "run not found")
	case err != nil:
		return nil, a.fail(ctx, "get report", err)
	}
	rep, err := report.Decode(data)
	if err != nil {
		return nil, a.fail(ctx, "decode stored report", err)
	}
	var buf bytes.Buffer
	if err := report.Render(&buf, rep, format, report.Options{Color: false}); err != nil {
		return nil, a.fail(ctx, "render report", err)
	}
	f := reportFormats[format]
	return &ReportFile{Body: buf.Bytes(), ContentType: f.contentType, Filename: run.PublicID + "-report." + f.ext}, nil
}

// handleReport serves GET /api/v1/runs/{id}/report?format=json|markdown|text
// as an attachment.
func (a *API) handleReport(w http.ResponseWriter, r *http.Request) {
	f, e := a.RenderReport(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("format"))
	if e != nil {
		WriteError(w, e)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Filename}))
	h.Set("Content-Length", strconv.Itoa(len(f.Body)))
	w.WriteHeader(http.StatusOK)
	w.Write(f.Body) //nolint:errcheck // client went away
}
