// Package cli implements the backendlab command line. Reports go to
// stdout (or --output); diagnostics go to stderr. Exit codes follow the
// documented policy: 0 passed, 1 checks failed, 2 invalid input,
// configuration or execution error.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/version"
)

// Exit codes.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitError  = 2
)

// App holds the process environment for one invocation. Tests construct it
// with buffers and a fake environment.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Env    config.EnvLookup
	// StdoutIsTerminal enables color by default when true.
	StdoutIsTerminal bool
	// Stdin is used by commands that read secrets (hash-password).
	Stdin io.Reader
	// Transport, when set, replaces the HTTP transport built for outgoing
	// requests. Tests and the demonstration scenario use it; the binary
	// leaves it nil.
	Transport http.RoundTripper
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, app *App, args []string) int
}

func (a *App) commands() []command {
	return []command{
		{"run", "Run a suite against one target and evaluate assertions", runCommand},
		{"compare", "Run a suite against baseline and candidate targets and compare responses", compareCommand},
		{"stream", "Inspect server-sent event streams (inspect FILE, probe URL)", streamCommand},
		{"validate", "Validate extracted JSON documents against rules and schemas", validateCommand},
		{"plans", "Compare two supplied PostgreSQL EXPLAIN (FORMAT JSON) plans", plansCommand},
		{"serve", "Run the authenticated API and dashboard (server mode)", serveCommand},
		{"migrate", "Apply database migrations (server mode)", migrateCommand},
		{"worker", "Execute queued background runs (server mode)", workerCommand},
		{"hash-password", "Hash the operator password read from stdin", hashPasswordCommand},
		{"retention", "Delete finished runs past the retention period (retention cleanup)", retentionCommand},
		{"version", "Print the version", versionCommand},
	}
}

// Run executes the command line args (without the program name) and
// returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.usage(a.Stderr)
		return ExitError
	}
	name, rest := args[0], args[1:]
	switch name {
	case "-h", "--help", "help":
		a.usage(a.Stdout)
		return ExitOK
	case "--version", "-version":
		return versionCommand(ctx, a, nil)
	}
	for _, c := range a.commands() {
		if c.name == name {
			return c.run(ctx, a, rest)
		}
	}
	a.errorf("unknown command %q (run 'backendlab help')", name)
	return ExitError
}

func (a *App) usage(w io.Writer) {
	fmt.Fprintf(w, "%s %s: reproduce and investigate regressions in backend and AI services\n\n", version.Name, version.Version)
	fmt.Fprintln(w, "Usage: backendlab <command> [arguments] [flags]")
	fmt.Fprintln(w, "\nCommands:")
	cmds := a.commands()
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].name < cmds[j].name })
	width := 0
	for _, c := range cmds {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.summary)
	}
	fmt.Fprintln(w, "\nExit codes: 0 checks passed, 1 checks failed, 2 invalid input or execution error.")
	fmt.Fprintln(w, "Run 'backendlab <command> -h' for command flags.")
}

// errorf writes a diagnostic line to stderr.
func (a *App) errorf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "backendlab: "+format+"\n", args...)
}

// logger returns a text logger on stderr for diagnostics. Reports never go
// through it.
func (a *App) logger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: level}))
}

// usageError marks invalid command-line usage (exit code 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// fail reports err on stderr and returns ExitError.
func (a *App) fail(err error) int {
	var ue *usageError
	if errors.As(err, &ue) {
		a.errorf("%s", ue.msg)
		return ExitError
	}
	msg := strings.TrimSpace(err.Error())
	a.errorf("%s", msg)
	return ExitError
}
