package cli

import (
	"context"
)

const compareUsage = "compare SUITE [--baseline NAME] [--candidate NAME] [--allow-writes] [--base-url NAME=URL] [--concurrency N] [--format F] [--output FILE] [--config FILE] [--no-color]"

// compareCommand executes a suite against a baseline and a candidate
// target, evaluates assertions on both, and compares their responses.
func compareCommand(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("compare", app.Stderr, compareUsage)
	var sf suiteFlags
	sf.register(fs)
	baseline := fs.String("baseline", "baseline", "baseline target name")
	candidate := fs.String("candidate", "candidate", "candidate target name")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 1 {
		return app.fail(usagef("compare needs exactly one suite file (usage: backendlab %s)", compareUsage))
	}
	if *baseline == *candidate {
		return app.fail(usagef("--baseline and --candidate must name different targets"))
	}
	sr, err := app.prepareSuite(sf, pos[0])
	if err != nil {
		return app.fail(err)
	}
	b, err := app.resolveTarget(sr.cfg, *baseline, sf.baseURLs)
	if err != nil {
		return app.fail(err)
	}
	c, err := app.resolveTarget(sr.cfg, *candidate, sf.baseURLs)
	if err != nil {
		return app.fail(err)
	}
	res, err := sr.exec.Compare(ctx, sr.loaded, b, c)
	return app.finishRun(ctx, sr, res, err)
}
