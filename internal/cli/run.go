package cli

import (
	"context"
)

const runUsage = "run SUITE --target NAME [--allow-writes] [--base-url NAME=URL] [--concurrency N] [--format F] [--output FILE] [--config FILE] [--no-color]"

// runCommand executes a suite against one target and evaluates the
// assertions of every case.
func runCommand(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("run", app.Stderr, runUsage)
	var sf suiteFlags
	sf.register(fs)
	target := fs.String("target", "", "target name (required)")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 1 {
		return app.fail(usagef("run needs exactly one suite file (usage: backendlab %s)", runUsage))
	}
	if *target == "" {
		return app.fail(usagef("--target is required"))
	}
	sr, err := app.prepareSuite(sf, pos[0])
	if err != nil {
		return app.fail(err)
	}
	t, err := app.resolveTarget(sr.cfg, *target, sf.baseURLs)
	if err != nil {
		return app.fail(err)
	}
	res, err := sr.exec.Run(ctx, sr.loaded, t)
	return app.finishRun(ctx, sr, res, err)
}
