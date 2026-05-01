package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
)

// maxDirEntries bounds how many directory entries are read, suite files
// or not, so a directory with a huge number of files fails quickly.
const maxDirEntries = 5000

// resolveRoot returns the absolute, symlink-free form of dir and checks
// that it is a directory.
func resolveRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("catalog: suites directory is not configured")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("catalog: suites directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("catalog: suites directory: %w", err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("catalog: suites directory: %w", err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("catalog: suites directory %s is not a directory", dir)
	}
	return root, nil
}

// suiteFiles returns the names of *.yaml and *.yml entries directly in
// root, sorted. Hidden files are ignored. Subdirectories are not searched.
func suiteFiles(root string) ([]string, error) {
	f, err := os.Open(root)
	if err != nil {
		return nil, fmt.Errorf("catalog: open suites directory: %w", err)
	}
	defer f.Close()
	entries, err := f.ReadDir(maxDirEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("catalog: read suites directory: %w", err)
	}
	if len(entries) > maxDirEntries {
		return nil, fmt.Errorf("catalog: suites directory has more than %d entries", maxDirEntries)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !isSuiteName(name) {
			continue
		}
		names = append(names, name)
	}
	if len(names) > MaxSuites {
		return nil, fmt.Errorf("catalog: suites directory holds %d suite files; the limit is %d", len(names), MaxSuites)
	}
	sort.Strings(names)
	return names, nil
}

func isSuiteName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

// loadEntry reads and validates one suite file. The bytes are read once:
// the same slice is validated, digested and kept as the snapshot.
func loadEntry(root, name string, lim limits.Limits) (Entry, error) {
	path, err := resolveInside(root, name)
	if err != nil {
		return Entry{}, err
	}
	data, err := readBounded(path, lim.MaxSuiteBytes)
	if err != nil {
		return Entry{}, err
	}
	s, err := suite.Parse(data, lim)
	if err != nil {
		return Entry{}, err
	}
	if err := checkReferences(root, s); err != nil {
		return Entry{}, err
	}
	return Entry{
		File:          name,
		Name:          s.Name,
		Description:   s.Description,
		Cases:         len(s.Cases),
		Mutating:      s.Mutating(),
		MutatingCases: s.MutatingCases(),
		SHA256:        suite.Digest(data),
		snapshot:      data,
	}, nil
}

// resolveInside resolves root/name, following symlinks, and fails unless
// the result is a regular file inside root.
func resolveInside(root, name string) (string, error) {
	p, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	if !within(root, p) {
		return "", fmt.Errorf("%s resolves outside the suites directory", name)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", name)
	}
	return p, nil
}

// within reports whether p is root or a path below it. Both must be
// absolute and symlink-free.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open suite: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, max)
	if errors.Is(err, jsonutil.ErrTooLarge) {
		return nil, fmt.Errorf("suite exceeds %d bytes", max)
	}
	if err != nil {
		return nil, fmt.Errorf("read suite: %w", err)
	}
	return data, nil
}

// checkReferences confines the schema and rules files a suite names to
// the suites directory and checks that they exist, so a suite that would
// fail at execution time is reported when the catalog loads.
func checkReferences(root string, s *suite.Suite) error {
	for i := range s.Cases {
		c := &s.Cases[i]
		refs := []string{c.Expect.Schema, c.Expect.Rules}
		if c.Stream != nil {
			refs = append(refs, c.Stream.Schema)
		}
		for _, ref := range refs {
			if ref == "" {
				continue
			}
			p, err := suite.ResolveFile(root, ref, true)
			if err != nil {
				return fmt.Errorf("case %q: %w", c.ID, err)
			}
			if _, err := resolveInside(root, mustRel(root, p)); err != nil {
				return fmt.Errorf("case %q: file reference %q: %w", c.ID, ref, err)
			}
		}
	}
	return nil
}

// mustRel returns p relative to root; ResolveFile has already confined it.
func mustRel(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return rel
}
