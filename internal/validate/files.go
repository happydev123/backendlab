package validate

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// resolveFile resolves a file referenced by a rules file relative to dir.
//
// With confine set (server mode) the reference must be relative and the
// resolved file must stay inside dir, both lexically and after symbolic
// links are resolved, so an uploaded or approved rules file cannot read
// arbitrary files on the host.
func resolveFile(dir, ref string, confine bool) (string, error) {
	if ref == "" {
		return "", errors.New("empty file reference")
	}
	if !confine {
		if filepath.IsAbs(ref) {
			return filepath.Clean(ref), nil
		}
		return filepath.Join(dir, ref), nil
	}
	if dir == "" {
		return "", fmt.Errorf("file reference %q needs a rules directory when files are confined", ref)
	}
	if filepath.IsAbs(ref) || filepath.VolumeName(ref) != "" || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, `\`) {
		return "", fmt.Errorf("file reference %q must be relative to the rules file", ref)
	}
	p := filepath.Join(dir, ref)
	if !inside(dir, p) {
		return "", fmt.Errorf("file reference %q escapes the rules directory", ref)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("rules directory: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("file reference %q: %w", ref, err)
	}
	if !inside(realDir, realPath) {
		return "", fmt.Errorf("file reference %q resolves outside the rules directory", ref)
	}
	return p, nil
}

// inside reports whether p is dir or a path below it.
func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
