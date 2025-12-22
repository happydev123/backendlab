package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// commonFlags are accepted by every report-producing command.
type commonFlags struct {
	format     string
	output     string
	configPath string
	noColor    bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.format, "format", "", "report format: text, json or markdown (default text)")
	fs.StringVar(&c.output, "output", "", "write the report to this file instead of stdout")
	fs.StringVar(&c.configPath, "config", "", "configuration file (default ./backendlab.yaml when present)")
	fs.BoolVar(&c.noColor, "no-color", false, "disable colored text output")
}

// newFlagSet creates a flag set that reports errors instead of exiting.
func newFlagSet(name string, stderr io.Writer, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: backendlab %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}
	return fs
}

// errHelp is returned when -h was requested; commands exit 0.
var errHelp = flag.ErrHelp

// parseArgs parses flags that may appear before, between or after
// positional arguments ("backendlab run suite.yaml --target candidate").
// "--" ends flag parsing.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		if rest[0] == "--" {
			positional = append(positional, rest[1:]...)
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	return positional, nil
}

// handleParseError converts flag parsing errors into exit codes.
func handleParseError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if errors.Is(err, errHelp) {
		return ExitOK, true
	}
	return ExitError, true
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
