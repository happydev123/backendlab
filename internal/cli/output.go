package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/report"
)

// outputSettings resolves the report format and color from flags,
// configuration and environment. Flags win over configuration.
type outputSettings struct {
	format report.Format
	color  bool
	path   string
}

func (a *App) resolveOutput(cf commonFlags, cfg *config.CLI) (outputSettings, error) {
	name := cf.format
	if name == "" && cfg != nil {
		name = cfg.Output.Format
	}
	f, err := report.ParseFormat(name)
	if err != nil {
		return outputSettings{}, usagef("%v", err)
	}
	color := a.StdoutIsTerminal && cf.output == "" && f == report.FormatText
	if cf.noColor || (cfg != nil && cfg.Output.NoColor) {
		color = false
	}
	if v, ok := a.lookupEnv("NO_COLOR"); ok && v != "" {
		color = false
	}
	return outputSettings{format: f, color: color, path: cf.output}, nil
}

func (a *App) lookupEnv(name string) (string, bool) {
	if a.Env == nil {
		return "", false
	}
	return a.Env(name)
}

// writeReport renders r to stdout or to the --output file. The file is
// written atomically (temporary file then rename) so a failed run never
// leaves a half-written report behind.
func (a *App) writeReport(r *report.Report, out outputSettings) error {
	var buf bytes.Buffer
	if err := report.Render(&buf, r, out.format, report.Options{Color: out.color}); err != nil {
		return err
	}
	if out.path == "" {
		_, err := a.Stdout.Write(buf.Bytes())
		return err
	}
	dir := filepath.Dir(out.path)
	tmp, err := os.CreateTemp(dir, ".backendlab-report-*")
	if err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write report: %w", err)
	}
	if err := os.Rename(tmp.Name(), out.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// finish writes the report and returns its exit code, or ExitError when
// the report cannot be written.
func (a *App) finish(r *report.Report, out outputSettings) int {
	if err := a.writeReport(r, out); err != nil {
		return a.fail(err)
	}
	if out.path != "" {
		fmt.Fprintf(a.Stderr, "report written to %s (status %s)\n", out.path, r.Status)
	}
	return r.ExitCode()
}

// loadConfig reads --config, or ./backendlab.yaml when present.
func (a *App) loadConfig(path string) (*config.CLI, error) {
	if path != "" {
		return config.LoadCLI(path, true)
	}
	return config.LoadCLI(config.DefaultCLIFile, false)
}
