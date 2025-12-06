// Package replay executes suite cases against HTTP targets. It builds each
// request from the target and the case, sends it once (nothing is ever
// retried), reads the response within configured bounds, evaluates the
// case's assertions and captures, and assembles a report. In compare mode
// each case is sent to a baseline and a candidate target and the responses
// are compared with package compare.
package replay

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
)

// maxCAFileBytes bounds the size of an extra CA bundle.
const maxCAFileBytes = 1 << 20

// Connection-level timeouts. Per-request and whole-run deadlines come from
// contexts; these only bound the phases a context deadline would otherwise
// leave to the operating system defaults.
const (
	dialTimeout         = 10 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	idleConnTimeout     = 30 * time.Second
	maxHeaderBytes      = 64 << 10
)

// NewTransport returns an HTTP transport for replay. TLS certificates are
// always verified (there is no option to disable verification) and TLS
// 1.2 is the minimum version. caFile, when set, names a PEM bundle whose
// certificates are trusted in addition to the system roots. Idle
// connections are bounded by lim.MaxConcurrency per host. Proxy settings
// from the environment are ignored so that requests go where the target
// says, which also keeps server-side egress checks meaningful.
func NewTransport(lim limits.Limits, caFile string) (*http.Transport, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pool, err := loadCertPool(caFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	perHost := lim.MaxConcurrency
	if perHost <= 0 {
		perHost = limits.Default().MaxConcurrency
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSClientConfig:        tlsCfg,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           perHost * 2,
		MaxIdleConnsPerHost:    perHost,
		MaxConnsPerHost:        perHost * 2,
		IdleConnTimeout:        idleConnTimeout,
		MaxResponseHeaderBytes: maxHeaderBytes,
		ExpectContinueTimeout:  time.Second,
	}, nil
}

// loadCertPool returns the system roots plus the certificates in file.
func loadCertPool(file string) (*x509.CertPool, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("open CA file: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, maxCAFileBytes)
	if err != nil {
		if errors.Is(err, jsonutil.ErrTooLarge) {
			return nil, fmt.Errorf("CA file %s exceeds %d bytes", file, maxCAFileBytes)
		}
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("CA file %s contains no PEM certificates", file)
	}
	return pool, nil
}
