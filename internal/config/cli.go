package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/yamlutil"
)

// DefaultCLIFile is read from the working directory when --config is not
// given and the file exists.
const DefaultCLIFile = "backendlab.yaml"

// maxConfigBytes bounds configuration files.
const maxConfigBytes = 1 << 20

// CLI is the command-line configuration file.
type CLI struct {
	Version int               `yaml:"version"`
	Output  Output            `yaml:"output"`
	Targets map[string]Target `yaml:"targets"`
	Limits  limits.Limits     `yaml:"limits"`
	Redact  redact.Policy     `yaml:"redact"`
}

// Output holds report defaults; flags override them.
type Output struct {
	Format  string `yaml:"format"`
	NoColor bool   `yaml:"no_color"`
}

// DefaultCLI returns the configuration used when no file is present.
func DefaultCLI() *CLI {
	return &CLI{Version: 1, Targets: map[string]Target{}, Limits: limits.Default()}
}

// LoadCLI reads path. With explicit == false a missing file yields the
// defaults (used for the implicit ./backendlab.yaml).
func LoadCLI(path string, explicit bool) (*CLI, error) {
	data, err := readConfig(path)
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return DefaultCLI(), nil
		}
		return nil, err
	}
	return ParseCLI(data)
}

// ParseCLI decodes and validates CLI configuration.
func ParseCLI(data []byte) (*CLI, error) {
	var c CLI
	if err := yamlutil.DecodeStrict(data, &c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("config: version must be 1 (got %d)", c.Version)
	}
	if c.Output.Format != "" {
		if _, err := report.ParseFormat(c.Output.Format); err != nil {
			return nil, fmt.Errorf("config: output.format: %w", err)
		}
	}
	for _, name := range sortedKeys(c.Targets) {
		if err := c.Targets[name].Validate(name); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	c.Limits = c.Limits.Merge()
	if err := c.Limits.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := redact.New(c.Redact); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

// Redactor builds the configured redactor.
func (c *CLI) Redactor() *redact.Redactor {
	r, err := redact.New(c.Redact)
	if err != nil {
		// ParseCLI validated the policy; DefaultCLI has an empty policy.
		return redact.Default()
	}
	return r
}

func readConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, maxConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return data, nil
}
