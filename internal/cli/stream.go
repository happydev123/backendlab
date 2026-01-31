package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/replay"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/stream"
	"github.com/happydev123/backendlab/internal/suite"
	"github.com/happydev123/backendlab/internal/version"
)

const (
	streamInspectUsage = "stream inspect FILE [expectation flags] [--format F] [--output FILE] [--config FILE] [--no-color]"
	streamProbeUsage   = "stream probe (URL | --target NAME --path /PATH) [--record FILE] [--timeout D] [expectation flags] [--format F] [--output FILE]"
)

// streamExpectFlags mirror the stream section of a suite case.
type streamExpectFlags struct {
	required        stringList
	terminalEvent   string
	terminalData    string
	requireTerminal bool
	jsonPayloads    bool
	schema          string
	maxGap          time.Duration
	maxDuration     time.Duration
	adapter         string
}

func (s *streamExpectFlags) register(fs *flag.FlagSet) {
	fs.Var(&s.required, "required-event", "event type that must appear (repeatable)")
	fs.StringVar(&s.terminalEvent, "terminal-event", "", "event type that marks application completion")
	fs.StringVar(&s.terminalData, "terminal-data", "", "data payload that marks completion, such as [DONE]")
	fs.BoolVar(&s.requireTerminal, "require-terminal", false, "fail when the stream ends without the terminal marker")
	fs.BoolVar(&s.jsonPayloads, "json-payloads", false, "require every data payload (except the terminal marker) to be JSON")
	fs.StringVar(&s.schema, "schema", "", "JSON Schema file for data payloads (implies --json-payloads)")
	fs.DurationVar(&s.maxGap, "max-gap", 0, "maximum allowed gap between events (live or timed recordings only)")
	fs.DurationVar(&s.maxDuration, "max-duration", 0, "maximum allowed stream duration")
	fs.StringVar(&s.adapter, "adapter", "", "optional payload adapter: chat-delta")
}

func (s *streamExpectFlags) expectations() (stream.Expectations, error) {
	spec := &suite.StreamSpec{
		RequiredEvents:  s.required,
		TerminalEvent:   s.terminalEvent,
		TerminalData:    s.terminalData,
		RequireTerminal: s.requireTerminal,
		JSONPayloads:    s.jsonPayloads || s.schema != "",
		Schema:          s.schema,
		MaxGap:          suite.Duration(s.maxGap),
		MaxDuration:     suite.Duration(s.maxDuration),
		Adapter:         s.adapter,
	}
	if spec.RequireTerminal && spec.TerminalEvent == "" && spec.TerminalData == "" {
		return stream.Expectations{}, usagef("--require-terminal needs --terminal-event or --terminal-data")
	}
	if spec.Adapter != "" && spec.Adapter != stream.AdapterChatDelta {
		return stream.Expectations{}, usagef("unsupported adapter %q (supported: %s)", spec.Adapter, stream.AdapterChatDelta)
	}
	if s.maxGap < 0 || s.maxDuration < 0 {
		return stream.Expectations{}, usagef("durations must not be negative")
	}
	// Schema paths given on the command line are relative to the working
	// directory, so resolve against ".".
	return stream.ExpectationsFromSpec(spec, ".")
}

func streamCommand(ctx context.Context, app *App, args []string) int {
	if len(args) == 0 {
		return app.fail(usagef("stream needs a subcommand: inspect or probe"))
	}
	switch args[0] {
	case "inspect":
		return streamInspect(ctx, app, args[1:])
	case "probe":
		return streamProbe(ctx, app, args[1:])
	case "-h", "--help", "help":
		fmt.Fprintf(app.Stdout, "Usage:\n  backendlab %s\n  backendlab %s\n", streamInspectUsage, streamProbeUsage)
		return ExitOK
	}
	return app.fail(usagef("unknown stream subcommand %q (want inspect or probe)", args[0]))
}

// streamInspect inspects a saved SSE file or timed recording offline.
func streamInspect(_ context.Context, app *App, args []string) int {
	fs := newFlagSet("stream inspect", app.Stderr, streamInspectUsage)
	var cf commonFlags
	cf.register(fs)
	var ef streamExpectFlags
	ef.register(fs)
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 1 {
		return app.fail(usagef("stream inspect needs exactly one file"))
	}
	cfg, err := app.loadConfig(cf.configPath)
	if err != nil {
		return app.fail(err)
	}
	out, err := app.resolveOutput(cf, cfg)
	if err != nil {
		return app.fail(err)
	}
	exp, err := ef.expectations()
	if err != nil {
		return app.fail(err)
	}
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return app.fail(err)
	}
	if int64(len(data)) > cfg.Limits.MaxStreamBytes {
		return app.fail(fmt.Errorf("%s exceeds limits.max_stream_bytes (%d)", pos[0], cfg.Limits.MaxStreamBytes))
	}
	res, err := stream.InspectReader(strings.NewReader(string(data)), pos[0], exp, stream.Options{
		Limits:   cfg.Limits,
		Redactor: cfg.Redactor(),
	})
	if err != nil {
		return app.fail(err)
	}
	r := res.Report(report.Input{Kind: "stream", Name: pos[0], SHA256: suite.Digest(data)})
	return app.finish(r, out)
}

// streamProbe requests a live stream with GET and inspects it as it
// arrives, optionally saving a timed recording.
func streamProbe(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("stream probe", app.Stderr, streamProbeUsage)
	var cf commonFlags
	cf.register(fs)
	var ef streamExpectFlags
	ef.register(fs)
	targetName := fs.String("target", "", "configured target to request")
	path := fs.String("path", "", "path below the target base URL (with --target)")
	record := fs.String("record", "", "save a timed recording of the stream to this file")
	timeout := fs.Duration("timeout", 30*time.Second, "overall time limit for the stream")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	cfg, err := app.loadConfig(cf.configPath)
	if err != nil {
		return app.fail(err)
	}
	out, err := app.resolveOutput(cf, cfg)
	if err != nil {
		return app.fail(err)
	}
	exp, err := ef.expectations()
	if err != nil {
		return app.fail(err)
	}
	if *timeout <= 0 || *timeout > cfg.Limits.MaxRequestTime {
		return app.fail(usagef("--timeout must be positive and at most %s", cfg.Limits.MaxRequestTime))
	}

	var reqURL *url.URL
	header := http.Header{}
	secrets := []string{}
	switch {
	case *targetName != "" && len(pos) == 0:
		if !strings.HasPrefix(*path, "/") {
			return app.fail(usagef("--path must start with '/'"))
		}
		t, err := app.resolveTarget(cfg, *targetName, nil)
		if err != nil {
			return app.fail(err)
		}
		u := *t.BaseURL
		u.Path = strings.TrimSuffix(u.Path, "/") + *path
		reqURL = &u
		header = t.Headers.Clone()
		secrets = t.Secrets
	case *targetName == "" && len(pos) == 1:
		u, err := url.Parse(pos[0])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return app.fail(usagef("probe URL must be an absolute http or https URL"))
		}
		if u.User != nil {
			return app.fail(usagef("probe URL must not contain credentials; use a configured target with env headers"))
		}
		reqURL = u
	default:
		return app.fail(usagef("give either a URL or --target with --path"))
	}
	red := cfg.Redactor().WithSecrets(secrets...)

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return app.fail(err)
	}
	req.Header = header
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", version.Name+"/"+version.Version)
	var transport http.RoundTripper = app.Transport
	if transport == nil {
		t, err := replay.NewTransport(cfg.Limits, "")
		if err != nil {
			return app.fail(err)
		}
		defer t.CloseIdleConnections()
		transport = t
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return app.fail(fmt.Errorf("request failed: %s", red.Error(err)))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return app.fail(fmt.Errorf("unexpected status %d from %s", resp.StatusCode, red.URL(reqURL)))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		app.errorf("warning: Content-Type is %q, not text/event-stream; parsing anyway", ct)
	}

	opts := stream.Options{Limits: cfg.Limits, Redactor: red, Start: start}
	var body interface{ Read([]byte) (int, error) } = resp.Body
	var rec *stream.Recorder
	if *record != "" {
		f, err := os.Create(*record)
		if err != nil {
			return app.fail(err)
		}
		defer f.Close()
		rec, err = stream.NewRecorder(resp.Body, f, opts)
		if err != nil {
			return app.fail(err)
		}
		body = rec
	}
	res := stream.ReadLive(ctx, body, exp, opts)
	resp.Body.Close()
	if rec != nil {
		if err := rec.Finish(res.Termination); err != nil {
			app.errorf("warning: recording incomplete: %v", err)
		}
	}
	r := res.Report(report.Input{Kind: "stream-url", Name: red.URL(reqURL)})
	r.Command = "stream.probe"
	return app.finish(r, out)
}
