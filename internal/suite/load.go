package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/yamlutil"
)

// Source describes where a suite was loaded from.
type Source struct {
	Path   string `json:"path"`
	Dir    string `json:"-"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Loaded is a validated suite with its source.
type Loaded struct {
	Suite  *Suite
	Source Source
}

// LoadFile reads, decodes and validates the suite at path. The file must
// not exceed lim.MaxSuiteBytes.
func LoadFile(path string, lim limits.Limits) (*Loaded, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open suite: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, lim.MaxSuiteBytes)
	if err != nil {
		if errors.Is(err, jsonutil.ErrTooLarge) {
			return nil, fmt.Errorf("suite %s exceeds %d bytes", path, lim.MaxSuiteBytes)
		}
		return nil, fmt.Errorf("read suite: %w", err)
	}
	s, err := Parse(data, lim)
	if err != nil {
		return nil, fmt.Errorf("suite %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return &Loaded{Suite: s, Source: Source{
		Path:   path,
		Dir:    filepath.Dir(abs),
		SHA256: Digest(data),
		Size:   int64(len(data)),
	}}, nil
}

// Parse decodes YAML suite data strictly and validates it. Unknown keys,
// duplicate keys and multiple YAML documents are rejected.
func Parse(data []byte, lim limits.Limits) (*Suite, error) {
	if int64(len(data)) > lim.MaxSuiteBytes {
		return nil, fmt.Errorf("suite exceeds %d bytes", lim.MaxSuiteBytes)
	}
	var s Suite
	if err := DecodeStrict(data, &s); err != nil {
		return nil, err
	}
	if err := Validate(&s, lim); err != nil {
		return nil, err
	}
	return &s, nil
}

// DecodeStrict decodes a single YAML document into dst, rejecting unknown
// fields and trailing documents.
func DecodeStrict(data []byte, dst any) error { return yamlutil.DecodeStrict(data, dst) }

// Digest returns the hex SHA-256 of data.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ResolveFile resolves a file referenced by the suite (schema or rules)
// relative to the suite directory. When confine is true the result must
// stay inside dir; server mode uses this so approved suites cannot read
// arbitrary server files.
func ResolveFile(dir, ref string, confine bool) (string, error) {
	if ref == "" {
		return "", errors.New("empty file reference")
	}
	if filepath.IsAbs(ref) {
		if confine {
			return "", fmt.Errorf("file reference %q must be relative to the suite", ref)
		}
		return filepath.Clean(ref), nil
	}
	p := filepath.Join(dir, ref)
	if confine {
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("file reference %q escapes the suite directory", ref)
		}
	}
	return p, nil
}
