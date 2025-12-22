// Command backendlab reproduces and investigates regressions in backend
// and AI services: HTTP replay and comparison, server-sent event
// inspection, structured extraction validation, offline query-plan
// comparison and optional PostgreSQL-backed background runs.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/happydev123/backendlab/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	app := &cli.App{
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		Stdin:            os.Stdin,
		Env:              os.LookupEnv,
		StdoutIsTerminal: isTerminal(os.Stdout),
	}
	code := app.Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// isTerminal reports whether f is a character device. It is a heuristic
// used only to decide whether to color text output by default.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
