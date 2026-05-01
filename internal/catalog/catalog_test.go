package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
)

const readOnlySuite = `version: 1
name: health-checks
description: Read-only checks.
cases:
  - id: health
    method: GET
    path: /health
  - id: version
    method: GET
    path: /version
`

const mutatingSuite = `version: 1
name: counter-writes
cases:
  - id: increment
    method: POST
    path: /counter/increment
  - id: read
    method: GET
    path: /counter
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestLoadDirListsValidSuites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "health.yaml", readOnlySuite)
	writeFile(t, dir, "writes.yml", mutatingSuite)
	writeFile(t, dir, "notes.txt", "not a suite")
	writeFile(t, dir, ".hidden.yaml", "version: [")
	writeFile(t, dir, "nested/deep.yaml", readOnlySuite)
	if err := os.Mkdir(filepath.Join(dir, "dir.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := Load(&config.Server{SuitesDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	list := c.List()
	if len(list) != 2 || list[0].File != "health.yaml" || list[1].File != "writes.yml" {
		t.Fatalf("entries = %+v", list)
	}
	h := list[0]
	if h.Name != "health-checks" || h.Description != "Read-only checks." || h.Cases != 2 || h.Mutating || len(h.SHA256) != 64 {
		t.Errorf("health entry = %+v", h)
	}
	if string(h.Snapshot()) != readOnlySuite {
		t.Error("snapshot differs from the file bytes")
	}
	w := list[1]
	if !w.Mutating || strings.Join(w.MutatingCases, ",") != "increment" {
		t.Errorf("writes entry = %+v", w)
	}
	if _, ok := c.Get("nested/deep.yaml"); ok {
		t.Error("suite in a subdirectory was approved")
	}
	if _, ok := c.Get("../health.yaml"); ok {
		t.Error("parent reference resolved")
	}
	// dir.yaml is a directory, not a regular file.
	probs := c.Problems()
	if len(probs) != 1 || probs[0].File != "dir.yaml" || !strings.Contains(probs[0].Error, "not a regular file") {
		t.Errorf("problems = %+v", probs)
	}
	// The snapshot is a copy; mutating it does not change the catalog.
	snap := h.Snapshot()
	snap[0] = 'X'
	if got, _ := c.Get("health.yaml"); string(got.Snapshot()) != readOnlySuite {
		t.Error("snapshot aliasing")
	}
}

func TestInvalidSuitesAreReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.yaml", readOnlySuite)
	writeFile(t, dir, "broken.yaml", "version: 1\nname: x\ncases: [\n")
	writeFile(t, dir, "unknown-key.yaml", readOnlySuite+"surprise: true\n")
	writeFile(t, dir, "empty.yaml", "")
	writeFile(t, dir, "big.yaml", readOnlySuite+"# "+strings.Repeat("x", 2048)+"\n")

	lim := limits.Default()
	lim.MaxSuiteBytes = 1024
	c, err := LoadDir(dir, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 1 {
		t.Fatalf("entries = %+v", c.List())
	}
	probs := c.Problems()
	files := make([]string, len(probs))
	for i, p := range probs {
		files[i] = p.File
		if p.Error == "" {
			t.Errorf("%s: empty error", p.File)
		}
	}
	if strings.Join(files, ",") != "big.yaml,broken.yaml,empty.yaml,unknown-key.yaml" {
		t.Errorf("problem files = %v", files)
	}
	if !strings.Contains(probs[0].Error, "exceeds 1024 bytes") {
		t.Errorf("oversize problem = %q", probs[0].Error)
	}
}

func TestSymlinkConfinement(t *testing.T) {
	outside := t.TempDir()
	secret := writeFile(t, outside, "outside.yaml", readOnlySuite)
	dir := t.TempDir()
	real := writeFile(t, dir, "real.yaml", mutatingSuite)
	symlinkOrSkip(t, secret, filepath.Join(dir, "escape.yaml"))
	symlinkOrSkip(t, real, filepath.Join(dir, "alias.yaml"))

	c, err := LoadDir(dir, limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("escape.yaml"); ok {
		t.Error("symlink pointing outside the suites directory was approved")
	}
	if e, ok := c.Get("alias.yaml"); !ok || e.Name != "counter-writes" {
		t.Errorf("symlink inside the directory: %+v, %v", e, ok)
	}
	probs := c.Problems()
	if len(probs) != 1 || probs[0].File != "escape.yaml" || !strings.Contains(probs[0].Error, "outside the suites directory") {
		t.Errorf("problems = %+v", probs)
	}
}

func TestSuitesDirectoryItselfMayBeASymlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "health.yaml", readOnlySuite)
	link := filepath.Join(t.TempDir(), "suites")
	symlinkOrSkip(t, dir, link)
	c, err := LoadDir(link, limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 1 || len(c.Problems()) != 0 {
		t.Errorf("entries %+v problems %+v", c.List(), c.Problems())
	}
}

func TestReferencedFilesAreConfined(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "schema.json", `{"type":"object"}`)
	dir := t.TempDir()
	writeFile(t, dir, "schemas/ok.json", `{"type":"object"}`)
	suiteWith := func(ref string) string {
		return "version: 1\nname: refs\ncases:\n  - id: a\n    method: GET\n    path: /a\n    expect:\n      schema: " + ref + "\n"
	}
	writeFile(t, dir, "ok.yaml", suiteWith("schemas/ok.json"))
	writeFile(t, dir, "parent.yaml", suiteWith("../"+filepath.Base(outside)+"/schema.json"))
	writeFile(t, dir, "missing.yaml", suiteWith("schemas/none.json"))

	c, err := LoadDir(dir, limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("ok.yaml"); !ok {
		t.Errorf("suite with a confined reference rejected: %+v", c.Problems())
	}
	probs := map[string]string{}
	for _, p := range c.Problems() {
		probs[p.File] = p.Error
	}
	if !strings.Contains(probs["parent.yaml"], "escapes") {
		t.Errorf("parent reference: %q", probs["parent.yaml"])
	}
	if probs["missing.yaml"] == "" {
		t.Error("missing reference accepted")
	}
}

func TestLoadDirFailures(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "absent"), limits.Default()); err == nil {
		t.Error("missing directory accepted")
	}
	file := writeFile(t, t.TempDir(), "file.yaml", readOnlySuite)
	if _, err := LoadDir(file, limits.Default()); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file as directory: %v", err)
	}
	if _, err := LoadDir("  ", limits.Default()); err == nil {
		t.Error("empty directory name accepted")
	}
	if _, err := Load(nil); err == nil {
		t.Error("nil config accepted")
	}

	many := t.TempDir()
	for i := 0; i <= MaxSuites; i++ {
		writeFile(t, many, fmt.Sprintf("s%03d.yaml", i), readOnlySuite)
	}
	if _, err := LoadDir(many, limits.Default()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("too many suites: %v", err)
	}
}
