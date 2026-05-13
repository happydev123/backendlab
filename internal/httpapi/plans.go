package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/plans"
	"github.com/happydev123/backendlab/internal/report"
)

// Plan upload bounds.
const (
	maxUploadParts    = 8
	maxSmallFieldSize = 256
	maxUploadNameLen  = 100
)

// PlanComparison is the result of comparing two uploaded plans. Nothing
// is persisted and no SQL is executed: the plans are EXPLAIN (FORMAT JSON)
// documents supplied by the operator.
type PlanComparison struct {
	Report *report.Report
	Before plans.Summary
	After  plans.Summary
}

// ComparePlans reads a multipart/form-data upload with the plan files
// "before" and "after" and an optional "fail_on" field (for example
// "scan,spill"), and compares them. The body is read part by part
// without temporary files; each plan is bounded by Limits.MaxPlanBytes.
//
// When checkCSRF is non-nil (dashboard forms, where the token cannot be in
// a header), the upload must start with a "csrf_token" part that
// checkCSRF accepts; plan files before it are refused.
func (a *API) ComparePlans(r *http.Request, checkCSRF func(token string) bool) (*PlanComparison, *Error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return nil, Errorf(http.StatusUnsupportedMediaType, CodeUnsupportedMedia, "upload must be multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "malformed multipart body")
	}
	lim := a.cfg.Limits.Merge()
	lim.MaxPlanBytes = a.limits.MaxPlanBytes
	var (
		parsed  = map[string]*plans.Plan{}
		inputs  = map[string]report.Input{}
		failOn  map[string]bool
		csrfOK  = checkCSRF == nil
		nParts  int
		csrfErr = Errorf(http.StatusForbidden, CodeCSRF, "missing or invalid CSRF token")
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, uploadError(err)
		}
		nParts++
		if nParts > maxUploadParts {
			return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "upload has more than %d parts", maxUploadParts)
		}
		name := part.FormName()
		switch name {
		case CSRFField:
			v, e := readSmallField(part)
			if e != nil {
				return nil, e
			}
			if checkCSRF != nil {
				if !checkCSRF(v) {
					return nil, csrfErr
				}
				csrfOK = true
			}
		case "fail_on":
			v, e := readSmallField(part)
			if e != nil {
				return nil, e
			}
			if failOn, err = plans.ParseFailOn(v); err != nil {
				return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "%s", err)
			}
		case "before", "after":
			if !csrfOK {
				return nil, csrfErr
			}
			if _, dup := parsed[name]; dup {
				return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "plan %q was uploaded twice", name)
			}
			data, err := jsonutil.ReadAllLimit(part, lim.MaxPlanBytes)
			if errors.Is(err, jsonutil.ErrTooLarge) {
				return nil, Errorf(http.StatusRequestEntityTooLarge, CodeTooLarge, "plan %q exceeds %d bytes", name, lim.MaxPlanBytes)
			}
			if err != nil {
				return nil, uploadError(err)
			}
			label := uploadName(part.FileName(), name)
			p, err := plans.Parse(data, label, lim)
			if err != nil {
				return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "%s plan: %s", name, err)
			}
			sum := sha256.Sum256(data)
			parsed[name] = p
			inputs[name] = report.Input{Kind: plans.InputKind, Name: label, SHA256: hex.EncodeToString(sum[:])}
		default:
			return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "unexpected form field %q", jsonutil.Truncate(name, 32))
		}
	}
	if !csrfOK {
		return nil, csrfErr
	}
	for _, name := range []string{"before", "after"} {
		if parsed[name] == nil {
			return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "the %q plan file is required", name)
		}
	}
	cmp := plans.Compare(parsed["before"], parsed["after"], plans.Options{FailOn: failOn, MaxFindings: lim.MaxFindings})
	return &PlanComparison{
		Report: cmp.Report([]report.Input{inputs["before"], inputs["after"]}),
		Before: plans.Summarize(parsed["before"]),
		After:  plans.Summarize(parsed["after"]),
	}, nil
}

func readSmallField(part io.Reader) (string, *Error) {
	data, err := jsonutil.ReadAllLimit(part, maxSmallFieldSize)
	if errors.Is(err, jsonutil.ErrTooLarge) {
		return "", Errorf(http.StatusBadRequest, CodeInvalidRequest, "form field exceeds %d bytes", maxSmallFieldSize)
	}
	if err != nil {
		return "", uploadError(err)
	}
	return strings.TrimSpace(string(data)), nil
}

// uploadError maps a multipart read error; exceeding the body bound is
// 413, anything else a malformed body.
func uploadError(err error) *Error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return Errorf(http.StatusRequestEntityTooLarge, CodeTooLarge, "upload exceeds %d bytes", tooBig.Limit)
	}
	return Errorf(http.StatusBadRequest, CodeInvalidRequest, "malformed multipart body")
}

// uploadName reduces a client-supplied file name to a short base name
// without control characters, for the report inputs.
func uploadName(fileName, field string) string {
	n := path.Base(strings.ReplaceAll(fileName, `\`, "/"))
	n = report.TerminalSafe(n)
	if n == "." || n == "/" || strings.TrimSpace(n) == "" {
		return field
	}
	return jsonutil.Truncate(n, maxUploadNameLen)
}

// handleComparePlans serves POST /api/v1/plans/compare and answers with
// the comparison report as JSON.
func (a *API) handleComparePlans(w http.ResponseWriter, r *http.Request) {
	cmp, e := a.ComparePlans(r, nil)
	if e != nil {
		WriteError(w, e)
		return
	}
	var buf bytes.Buffer
	if err := report.RenderJSON(&buf, cmp.Report); err != nil {
		WriteError(w, a.fail(r.Context(), "render plan report", err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes()) //nolint:errcheck // client went away
}
