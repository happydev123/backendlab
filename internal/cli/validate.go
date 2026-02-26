package cli

import (
	"context"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/schema"
	"github.com/happydev123/backendlab/internal/validate"
)

const validateUsage = "validate DOCUMENTS (--rules FILE | --schema FILE) [--format F] [--output FILE] [--config FILE] [--no-color]"

// validateCommand checks already-extracted JSON documents (one object, an
// array, or JSON Lines) against a rules file and/or a JSON Schema.
func validateCommand(_ context.Context, app *App, args []string) int {
	fs := newFlagSet("validate", app.Stderr, validateUsage)
	var cf commonFlags
	cf.register(fs)
	rulesPath := fs.String("rules", "", "ExtractCheck rules file")
	schemaPath := fs.String("schema", "", "JSON Schema file applied in addition to any schema named by the rules")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 1 {
		return app.fail(usagef("validate needs exactly one documents file (usage: backendlab %s)", validateUsage))
	}
	if *rulesPath == "" && *schemaPath == "" {
		return app.fail(usagef("give --rules, --schema, or both"))
	}
	cfg, err := app.loadConfig(cf.configPath)
	if err != nil {
		return app.fail(err)
	}
	out, err := app.resolveOutput(cf, cfg)
	if err != nil {
		return app.fail(err)
	}

	var inputs []report.Input
	var rules *validate.Rules
	if *rulesPath != "" {
		if rules, err = validate.LoadRules(*rulesPath); err != nil {
			return app.fail(err)
		}
		if in, ok := rules.Input(); ok {
			inputs = append(inputs, in)
		}
	}
	var sch *schema.Schema
	if *schemaPath != "" {
		if sch, err = schema.CompileFile(*schemaPath); err != nil {
			return app.fail(err)
		}
		inputs = append(inputs, report.Input{Kind: "schema", Name: *schemaPath})
	}
	v, err := validate.New(rules, sch, validate.Options{
		Redactor:        cfg.Redactor(),
		MaxFindings:     cfg.Limits.MaxFindings,
		MaxValuePreview: cfg.Limits.MaxValuePreview,
	})
	if err != nil {
		return app.fail(err)
	}
	docs, docInput, err := validate.LoadDocuments(pos[0], cfg.Limits)
	if err != nil {
		return app.fail(err)
	}
	inputs = append([]report.Input{docInput}, inputs...)
	res := v.Run(docs)
	r := validate.BuildReport(inputs, res.Documents, res.Findings, res.Dropped)
	return app.finish(r, out)
}
