package cli

import (
	"bytes"
	"context"
	"flag"
	"io"
	"strings"
	"testing"
)

// testApp returns an App writing to buffers with the given environment.
func testApp(env map[string]string) (*App, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	app := &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Env: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
	return app, &stdout, &stderr
}

func TestVersionAndHelp(t *testing.T) {
	app, stdout, stderr := testApp(nil)
	if code := app.Run(context.Background(), []string{"version"}); code != ExitOK {
		t.Fatalf("version exit = %d", code)
	}
	if !strings.HasPrefix(stdout.String(), "backendlab ") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}

	app, stdout, _ = testApp(nil)
	if code := app.Run(context.Background(), []string{"help"}); code != ExitOK {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "Exit codes:") {
		t.Fatalf("help output = %q", stdout)
	}
}

func TestUnknownCommandAndNoArgs(t *testing.T) {
	app, stdout, stderr := testApp(nil)
	if code := app.Run(context.Background(), []string{"frobnicate"}); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	app, _, _ = testApp(nil)
	if code := app.Run(context.Background(), nil); code != ExitError {
		t.Fatalf("no-args exit = %d", code)
	}
}

func TestParseArgsInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	target := fs.String("target", "", "")
	verbose := fs.Bool("v", false, "")
	pos, err := parseArgs(fs, []string{"suite.yaml", "--target", "candidate", "extra", "-v", "--", "-not-a-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if *target != "candidate" || !*verbose {
		t.Fatalf("flags: target=%q v=%v", *target, *verbose)
	}
	want := []string{"suite.yaml", "extra", "-not-a-flag"}
	if strings.Join(pos, "|") != strings.Join(want, "|") {
		t.Fatalf("positional = %q, want %q", pos, want)
	}
	if _, err := parseArgs(fs, []string{"--bogus"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestResolveOutputColor(t *testing.T) {
	app, _, _ := testApp(nil)
	app.StdoutIsTerminal = true
	out, err := app.resolveOutput(commonFlags{}, nil)
	if err != nil || !out.color {
		t.Fatalf("terminal default should color: %+v %v", out, err)
	}
	out, _ = app.resolveOutput(commonFlags{noColor: true}, nil)
	if out.color {
		t.Fatal("--no-color ignored")
	}
	out, _ = app.resolveOutput(commonFlags{format: "json"}, nil)
	if out.color {
		t.Fatal("json must never be colored")
	}
	app, _, _ = testApp(map[string]string{"NO_COLOR": "1"})
	app.StdoutIsTerminal = true
	out, _ = app.resolveOutput(commonFlags{}, nil)
	if out.color {
		t.Fatal("NO_COLOR ignored")
	}
	if _, err := app.resolveOutput(commonFlags{format: "xml"}, nil); err == nil {
		t.Fatal("invalid format accepted")
	}
}
