// Package catalog lists the approved suites that "backendlab serve" may
// run. Approved suites are the YAML files directly inside the configured
// suites directory (server config suites_dir). The catalog reads each file
// once, validates it and keeps the exact bytes it validated, so the
// snapshot stored with a run is always the suite that passed validation.
//
// The catalog is read when the server starts. Changes to the directory are
// picked up after a restart. Submissions name a suite by its file name and
// choose targets from the server configuration; they can never supply a
// URL, an environment variable name or suite content of their own.
package catalog

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
)

// MaxSuites bounds the number of suite files in the suites directory.
// Loading a directory with more fails instead of silently skipping files.
const MaxSuites = 200

// maxProblemBytes bounds the stored text of one load problem.
const maxProblemBytes = 2000

// Entry is one approved suite.
type Entry struct {
	// File is the file name inside the suites directory. It is the key
	// used by submissions and stored as the run's suite_file.
	File        string
	Name        string
	Description string
	Cases       int
	// Mutating is true when the suite has a case with a method outside
	// GET, HEAD and OPTIONS. MutatingCases lists those case IDs.
	Mutating      bool
	MutatingCases []string
	// SHA256 is the hex digest of the file bytes held in the catalog.
	SHA256 string

	snapshot []byte
}

// Snapshot returns a copy of the suite bytes that were validated.
func (e Entry) Snapshot() []byte { return bytes.Clone(e.snapshot) }

// Problem is a suite file that could not be loaded. The server keeps
// running; the dashboard shows these so the operator can fix them.
type Problem struct {
	File  string
	Error string
}

// Catalog is an immutable view of the approved suites. It is safe for
// concurrent use.
type Catalog struct {
	dir      string
	entries  []Entry
	byFile   map[string]int
	problems []Problem
}

// Load reads the suites directory named by cfg.SuitesDir with the
// configured limits. A relative directory is resolved against the
// process working directory.
func Load(cfg *config.Server) (*Catalog, error) {
	if cfg == nil {
		return nil, fmt.Errorf("catalog: server configuration is required")
	}
	return LoadDir(cfg.SuitesDir, cfg.Limits)
}

// LoadDir reads the approved suites in dir. It fails when dir cannot be
// read or holds more than MaxSuites suite files. Individual files that do
// not load are reported by Problems and left out of List.
func LoadDir(dir string, lim limits.Limits) (*Catalog, error) {
	lim = lim.Merge()
	root, err := resolveRoot(dir)
	if err != nil {
		return nil, err
	}
	names, err := suiteFiles(root)
	if err != nil {
		return nil, err
	}
	c := &Catalog{dir: root, byFile: map[string]int{}}
	for _, name := range names {
		e, err := loadEntry(root, name, lim)
		if err != nil {
			msg := err.Error()
			if len(msg) > maxProblemBytes {
				msg = msg[:maxProblemBytes] + " ..."
			}
			c.problems = append(c.problems, Problem{File: name, Error: msg})
			continue
		}
		c.byFile[e.File] = len(c.entries)
		c.entries = append(c.entries, e)
	}
	sort.Slice(c.problems, func(i, j int) bool { return c.problems[i].File < c.problems[j].File })
	return c, nil
}

// Dir returns the resolved absolute suites directory.
func (c *Catalog) Dir() string { return c.dir }

// List returns the approved suites ordered by file name.
func (c *Catalog) List() []Entry { return append([]Entry(nil), c.entries...) }

// Problems returns the files that failed to load, ordered by file name.
func (c *Catalog) Problems() []Problem { return append([]Problem(nil), c.problems...) }

// Get returns the approved suite stored under file.
func (c *Catalog) Get(file string) (Entry, bool) {
	i, ok := c.byFile[file]
	if !ok {
		return Entry{}, false
	}
	return c.entries[i], true
}
