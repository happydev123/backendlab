package store

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/report"
)

// Bounds on run metadata accepted by EnqueueRun.
const (
	maxSuiteNameLen  = 200
	maxSuiteFileLen  = 512
	maxTargets       = 16
	maxTargetNameLen = 100
	// MaxAttemptsLimit is the largest MaxAttempts a run may have; it
	// matches the bound on worker.max_read_only_attempts.
	MaxAttemptsLimit = 5
)

// Run is one row of the runs table, without the stored report bytes (use
// GetReport for those).
type Run struct {
	ID       int64
	PublicID string

	SuiteName string
	// SuiteFile is relative to the server's approved suites directory.
	SuiteFile   string
	SuiteSHA256 string
	// SuiteSnapshot is the suite YAML as approved at submission time. The
	// worker executes the snapshot, not the current file on disk.
	SuiteSnapshot []byte

	Mode            Mode
	Targets         []string
	WritesRequested bool
	// Mutating is true when the suite contains a case with a non read-only
	// method. Mutating runs are never retried automatically.
	Mutating bool

	Status      Status
	Attempt     int
	MaxAttempts int

	LeaseOwner string
	// LeaseToken is the fencing token of the latest claim.
	LeaseToken        int64
	LeaseExpiresAt    *time.Time
	HeartbeatAt       *time.Time
	CancelRequestedAt *time.Time

	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time

	// Error is a sanitized explanation for failed, cancelled and
	// interrupted runs, or a note about a truncated report.
	Error           string
	Summary         *report.Summary
	ReportTruncated bool
	// ResubmittedFrom is the run this one was resubmitted from, if any.
	ResubmittedFrom *int64
}

// CancelRequested reports whether an operator asked to cancel the run.
func (r *Run) CancelRequested() bool { return r.CancelRequestedAt != nil }

// NewRun describes a run to enqueue.
type NewRun struct {
	SuiteName       string
	SuiteFile       string
	SuiteSnapshot   []byte
	Mode            Mode
	Targets         []string
	WritesRequested bool
	Mutating        bool
	// MaxAttempts bounds automatic retries of read-only runs after a lost
	// lease (config worker.max_read_only_attempts). Zero means 1. Mutating
	// runs always get 1: they are never re-executed automatically.
	MaxAttempts int
}

// Validate checks the submission and returns the normalized value.
func (n NewRun) Validate() (NewRun, error) {
	if err := checkText("suite name", n.SuiteName, maxSuiteNameLen); err != nil {
		return n, err
	}
	file, err := cleanSuiteFile(n.SuiteFile)
	if err != nil {
		return n, err
	}
	n.SuiteFile = file
	if len(n.SuiteSnapshot) == 0 {
		return n, errors.New("suite snapshot is empty")
	}
	if max := limits.Hard().MaxSuiteBytes; int64(len(n.SuiteSnapshot)) > max {
		return n, fmt.Errorf("suite snapshot exceeds %d bytes", max)
	}
	if !n.Mode.Valid() {
		return n, fmt.Errorf("unknown mode %q", n.Mode)
	}
	if err := checkTargets(n.Mode, n.Targets); err != nil {
		return n, err
	}
	if n.Mutating && !n.WritesRequested {
		return n, errors.New("suite contains mutating cases but writes were not requested")
	}
	switch {
	case n.MaxAttempts == 0:
		n.MaxAttempts = 1
	case n.MaxAttempts < 0 || n.MaxAttempts > MaxAttemptsLimit:
		return n, fmt.Errorf("max attempts must be between 1 and %d", MaxAttemptsLimit)
	}
	if n.Mutating {
		n.MaxAttempts = 1
	}
	n.Targets = append([]string(nil), n.Targets...)
	return n, nil
}

func checkText(what, s string, max int) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if len(s) > max {
		return fmt.Errorf("%s exceeds %d bytes", what, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", what)
		}
	}
	return nil
}

// cleanSuiteFile normalizes a suites-directory-relative path to forward
// slashes and rejects absolute paths and parent references.
func cleanSuiteFile(p string) (string, error) {
	if err := checkText("suite file", p, maxSuiteFileLen); err != nil {
		return "", err
	}
	s := filepath.ToSlash(p)
	if path.IsAbs(s) || filepath.VolumeName(p) != "" || strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("suite file %q must be relative to the suites directory", p)
	}
	s = path.Clean(s)
	if s == "." || s == ".." || strings.HasPrefix(s, "../") {
		return "", fmt.Errorf("suite file %q escapes the suites directory", p)
	}
	return s, nil
}

func checkTargets(mode Mode, targets []string) error {
	switch {
	case mode == ModeCompare && len(targets) != 2:
		return errors.New("compare mode needs exactly two targets (baseline, candidate)")
	case len(targets) == 0:
		return errors.New("at least one target is required")
	case len(targets) > maxTargets:
		return fmt.Errorf("at most %d targets are allowed", maxTargets)
	}
	seen := map[string]bool{}
	for _, t := range targets {
		if err := checkText("target name", t, maxTargetNameLen); err != nil {
			return err
		}
		if seen[t] {
			return fmt.Errorf("target %q is listed twice", t)
		}
		seen[t] = true
	}
	return nil
}

// newPublicID returns "run_" followed by 16 random base32 characters.
func newPublicID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run_" + strings.ToLower(base32.StdEncoding.EncodeToString(b[:])), nil
}

// RunColumns is the column list matching ScanRun, for packages that select
// runs with their own SQL.
const RunColumns = `id, public_id, suite_name, suite_file, suite_sha256, suite_snapshot,
	mode, targets, writes_requested, mutating, status, attempt, max_attempts,
	lease_owner, lease_token, lease_expires_at, heartbeat_at, cancel_requested_at,
	created_at, started_at, finished_at, error, summary, report_truncated, resubmitted_from`

// ScanRun scans one row selected with RunColumns. It returns ErrNotFound
// when the row set is empty.
func ScanRun(row pgx.Row) (*Run, error) {
	var (
		r       Run
		owner   *string
		errText *string
		summary []byte
	)
	err := row.Scan(&r.ID, &r.PublicID, &r.SuiteName, &r.SuiteFile, &r.SuiteSHA256, &r.SuiteSnapshot,
		&r.Mode, &r.Targets, &r.WritesRequested, &r.Mutating, &r.Status, &r.Attempt, &r.MaxAttempts,
		&owner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt, &r.CancelRequestedAt,
		&r.CreatedAt, &r.StartedAt, &r.FinishedAt, &errText, &summary, &r.ReportTruncated, &r.ResubmittedFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan run: %w", err)
	}
	if owner != nil {
		r.LeaseOwner = *owner
	}
	if errText != nil {
		r.Error = *errText
	}
	if r.Summary, err = decodeSummary(summary); err != nil {
		return nil, err
	}
	return &r, nil
}

func decodeSummary(data []byte) (*report.Summary, error) {
	if data == nil {
		return nil, nil
	}
	var s report.Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode run summary: %w", err)
	}
	return &s, nil
}
