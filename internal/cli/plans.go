package cli

import (
	"context"
	"fmt"

	"github.com/happydev123/backendlab/internal/plans"
	"github.com/happydev123/backendlab/internal/report"
)

const plansCompareUsage = "plans compare BEFORE.json AFTER.json [--fail-on CLASSES] [--row-factor N] [--min-rows N] [--format F] [--output FILE] [--config FILE] [--no-color]"

// plansCommand dispatches plan subcommands. Plans are always supplied as
// files; BackendLab never connects to a database to collect them.
func plansCommand(ctx context.Context, app *App, args []string) int {
	if len(args) == 0 {
		return app.fail(usagef("plans needs a subcommand: compare"))
	}
	switch args[0] {
	case "compare":
		return plansCompare(ctx, app, args[1:])
	case "-h", "--help", "help":
		fmt.Fprintf(app.Stdout, "Usage:\n  backendlab %s\n\nfail-on classes: %v (or all)\n", plansCompareUsage, plans.FailOnClasses())
		return ExitOK
	}
	return app.fail(usagef("unknown plans subcommand %q (want compare)", args[0]))
}

func plansCompare(_ context.Context, app *App, args []string) int {
	fs := newFlagSet("plans compare", app.Stderr, plansCompareUsage)
	var cf commonFlags
	cf.register(fs)
	failOn := fs.String("fail-on", "", "comma-separated finding classes that make the command exit 1: scan, join, estimate, spill, temp_io, structure, all")
	rowFactor := fs.Float64("row-factor", 0, "estimate/actual row ratio reported as a mismatch (default 10)")
	minRows := fs.Int64("min-rows", 0, "ignore estimate mismatches when both row counts are below this (default 100)")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 2 {
		return app.fail(usagef("plans compare needs two plan files (usage: backendlab %s)", plansCompareUsage))
	}
	cfg, err := app.loadConfig(cf.configPath)
	if err != nil {
		return app.fail(err)
	}
	out, err := app.resolveOutput(cf, cfg)
	if err != nil {
		return app.fail(err)
	}
	classes, err := plans.ParseFailOn(*failOn)
	if err != nil {
		return app.fail(usagef("--fail-on: %v", err))
	}
	opts := plans.Options{RowFactor: *rowFactor, MinRows: *minRows, FailOn: classes, MaxFindings: cfg.Limits.MaxFindings}
	if err := opts.Validate(); err != nil {
		return app.fail(usagef("%v", err))
	}
	before, beforeIn, err := plans.ParseFile(pos[0], cfg.Limits)
	if err != nil {
		return app.fail(err)
	}
	after, afterIn, err := plans.ParseFile(pos[1], cfg.Limits)
	if err != nil {
		return app.fail(err)
	}
	beforeIn.Kind, afterIn.Kind = "plan-before", "plan-after"
	r := plans.Compare(before, after, opts).Report([]report.Input{beforeIn, afterIn})
	return app.finish(r, out)
}
