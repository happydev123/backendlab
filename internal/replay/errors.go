package replay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
)

// Transport error codes. A transport error means no complete response was
// obtained for a case on one target; the case status becomes error.
const (
	CodeTimeout          = "replay.timeout"
	CodeRunTimeout       = "replay.run_timeout"
	CodeCancelled        = "replay.cancelled"
	CodeConnectionFailed = "replay.connection_failed"
	CodeTLSFailed        = "replay.tls_failed"
	CodeResponseTooLarge = "replay.response_too_large"
	CodeTooManyRedirects = "replay.too_many_redirects"
	CodeRedirectBlocked  = "replay.redirect_blocked"
	CodeRequestInvalid   = "replay.request_invalid"
	CodeRequestFailed    = "replay.request_failed"
)

// contexts are the three scopes that can end a request: the caller's
// context, the whole-run timeout and the per-request timeout.
type contexts struct {
	parent, run, request context.Context
}

// classify maps a request or body-read error to a transport error code.
// Context state is checked first because a cancelled or expired context
// surfaces as many different low-level errors.
func classify(err error, cx contexts) string {
	switch {
	case cx.parent != nil && cx.parent.Err() != nil:
		if errors.Is(cx.parent.Err(), context.DeadlineExceeded) {
			return CodeRunTimeout
		}
		return CodeCancelled
	case cx.run != nil && cx.run.Err() != nil:
		return CodeRunTimeout
	case errors.Is(err, errTooManyRedirects):
		return CodeTooManyRedirects
	case isBlockedRedirect(err):
		return CodeRedirectBlocked
	case errors.Is(err, errResponseTooLarge), errors.Is(err, jsonutil.ErrTooLarge):
		return CodeResponseTooLarge
	case cx.request != nil && cx.request.Err() != nil, errors.Is(err, context.DeadlineExceeded), isNetTimeout(err):
		return CodeTimeout
	case errors.Is(err, context.Canceled):
		return CodeCancelled
	case isTLSError(err):
		return CodeTLSFailed
	case isConnectionError(err):
		return CodeConnectionFailed
	}
	return CodeRequestFailed
}

// transportMessage describes a transport error. Context-driven errors get
// a fixed message; other errors are rendered through the redactor so URLs
// with credentials and registered secrets never appear.
func transportMessage(code string, err error, r *redact.Redactor, timeout time.Duration, maxBytes int64) string {
	switch code {
	case CodeTimeout:
		return fmt.Sprintf("request did not complete within the %s timeout", timeout)
	case CodeRunTimeout:
		return "the run timeout expired while the request was in progress"
	case CodeCancelled:
		return "the run was cancelled while the request was in progress"
	case CodeResponseTooLarge:
		return fmt.Sprintf("response body exceeds the %d byte limit", maxBytes)
	}
	return r.Error(err)
}

func isBlockedRedirect(err error) bool {
	var blocked *redirectBlockedError
	return errors.As(err, &blocked)
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isTLSError(err error) bool {
	var (
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		verify   *tls.CertificateVerificationError
		record   tls.RecordHeaderError
		alert    tls.AlertError
	)
	return errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) ||
		errors.As(err, &verify) || errors.As(err, &record) || errors.As(err, &alert)
}

func isConnectionError(err error) bool {
	var (
		op  *net.OpError
		dns *net.DNSError
	)
	return errors.As(err, &op) || errors.As(err, &dns) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED)
}
