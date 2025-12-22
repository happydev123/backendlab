package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/version"
)

func versionCommand(_ context.Context, app *App, args []string) int {
	fs := newFlagSet("version", app.Stderr, "version")
	if _, err := parseArgs(fs, args); err != nil {
		code, _ := handleParseError(err)
		return code
	}
	fmt.Fprintf(app.Stdout, "%s %s (%s, %s/%s, report schema %s)\n",
		version.Name, version.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH, report.SchemaVersion)
	return ExitOK
}
