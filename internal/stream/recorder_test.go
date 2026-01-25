package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
)

func TestRecorderRoundTrip(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, end: 1500 * time.Millisecond, chunks: []timedChunk{
		{200 * time.Millisecond, ": hi\n\nevent: start\ndata: {\"n\":1}\n\ndata: caf\xC3"},
		{350 * time.Millisecond, "\xA9\n\n"},
		{600 * time.Millisecond, "data: <b>&</b> ©\n\n"},
		{1200 * time.Millisecond, "data: [DONE]\n\ndata: trailing"},
	}}
	opts := liveOpts(clock)
	var rec bytes.Buffer
	recorder, err := NewRecorder(body, &rec, opts)
	if err != nil {
		t.Fatal(err)
	}
	exp := Expectations{TerminalData: "[DONE]", RequiredEvents: []string{"start"}, MaxGap: 500 * time.Millisecond}
	live := ReadLive(context.Background(), recorder, exp, opts)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if live.Termination != TermTerminalEvent || live.EventCount != 4 {
		t.Fatalf("live = %+v", live)
	}
	lines := strings.Split(strings.TrimSpace(rec.String()), "\n")
	if lines[0] != `{"format":"backendlab.sse-recording/v1"}` || len(lines) != 6 {
		t.Fatalf("recording:\n%s", rec.String())
	}
	// The first two reads split a two-byte character, so each is stored
	// as base64; the third is valid UTF-8 and stays readable (HTML is not
	// escaped).
	if !strings.Contains(lines[1], `"chunk_b64"`) || !strings.Contains(lines[2], `"chunk_b64"`) ||
		lines[3] != "{\"offset_ms\":600,\"chunk\":\"data: <b>&</b> ©\\n\\n\"}" {
		t.Fatalf("records:\n%s", strings.Join(lines[1:4], "\n"))
	}
	// ReadLive stopped at the terminal event, so the body never ended.
	if lines[5] != `{"offset_ms":1200,"end":"stopped"}` {
		t.Fatalf("end record = %s", lines[5])
	}

	saved, err := InspectReader(bytes.NewReader(rec.Bytes()), "rec.jsonl", exp, Options{CaseID: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Termination != live.Termination || saved.EventCount != live.EventCount {
		t.Fatalf("saved = %+v", saved)
	}
	if !reflect.DeepEqual(saved.Events, live.Events) {
		t.Fatalf("events differ:\nlive  %+v\nsaved %+v", live.Events, saved.Events)
	}
	if *saved.FirstEvent != *live.FirstEvent || *saved.MaxGap != *live.MaxGap || *saved.Duration != *live.Duration {
		t.Fatal("timings differ between the live read and its recording")
	}
	if !reflect.DeepEqual(codes(saved.Findings), codes(live.Findings)) {
		t.Fatalf("findings differ:\nlive  %v\nsaved %v", codes(live.Findings), codes(saved.Findings))
	}
	if f := findCode(live.Findings, CodeMaxGapExceeded); f == nil || f.Location != "event:3" {
		t.Fatalf("findings = %+v", live.Findings)
	}
}

func codes(fs []finding.Finding) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Code+"@"+f.Location)
	}
	return out
}

func TestRecorderEOFAndFinish(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, end: 90 * time.Millisecond, chunks: []timedChunk{{10 * time.Millisecond, "data: a\n\n"}}}
	var rec bytes.Buffer
	recorder, err := NewRecorder(body, &rec, liveOpts(clock))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(recorder); err != nil {
		t.Fatal(err)
	}
	// The body ended, so Finish adds nothing.
	if err := recorder.Finish(TermTimeout); err != nil {
		t.Fatal(err)
	}
	want := `{"format":"backendlab.sse-recording/v1"}` + "\n" +
		`{"offset_ms":10,"chunk":"data: a\n\n"}` + "\n" +
		`{"offset_ms":90,"end":"eof"}` + "\n"
	if rec.String() != want {
		t.Fatalf("recording:\n%s", rec.String())
	}
	if recorder.Recorded() != int64(len("data: a\n\n")) {
		t.Fatalf("recorded = %d", recorder.Recorded())
	}
}

func TestRecorderFinishWithTermination(t *testing.T) {
	clock := newFakeClock()
	var rec bytes.Buffer
	recorder, err := NewRecorder(strings.NewReader(""), &rec, liveOpts(clock))
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(2500 * time.Microsecond)
	if err := recorder.Finish(TermTimeout); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rec.String(), `{"offset_ms":2.5,"end":"stopped","termination":"timeout"}`+"\n") {
		t.Fatalf("recording:\n%s", rec.String())
	}
	// A second Finish or Close does not write again.
	_ = recorder.Close()
	if strings.Count(rec.String(), `"end"`) != 1 {
		t.Fatalf("recording:\n%s", rec.String())
	}
}

func TestRecorderLimit(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, chunks: []timedChunk{
		{time.Millisecond, "data: a\n\n"},
		{2 * time.Millisecond, "data: bbbbbbbb\n\n"},
		{3 * time.Millisecond, "data: c\n\n"},
	}}
	opts := liveOpts(clock)
	opts.Limits = limits.Limits{MaxStreamBytes: 12}
	var rec bytes.Buffer
	recorder, err := NewRecorder(body, &rec, opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(recorder)
	if err != nil || len(data) != 9+16+9 {
		t.Fatalf("reads must pass through unchanged: %d bytes, %v", len(data), err)
	}
	lines := strings.Split(strings.TrimSpace(rec.String()), "\n")
	if len(lines) != 4 || lines[2] != `{"offset_ms":2,"chunk":"dat"}` || lines[3] != `{"offset_ms":2,"end":"limit_exceeded"}` {
		t.Fatalf("recording:\n%s", rec.String())
	}
	r, err := InspectReader(&rec, "rec.jsonl", Expectations{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Termination != TermLimitExceeded || r.EventCount != 1 || findCode(r.Findings, CodeRecordingTruncated) == nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestRecorderSplitsLargeReads(t *testing.T) {
	big := "data: " + strings.Repeat("x", maxRecordChunk+100) + "\n\n"
	var rec bytes.Buffer
	recorder, err := NewRecorder(strings.NewReader(big), &rec, Options{Limits: limits.Limits{MaxStreamBytes: 4 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(big))
	if n, err := io.ReadFull(recorder, buf); err != nil || n != len(big) {
		t.Fatalf("read %d, %v", n, err)
	}
	_ = recorder.Close()
	if got := strings.Count(rec.String(), `"chunk"`); got != 2 {
		t.Fatalf("chunk records = %d, want 2", got)
	}
	r, err := InspectReader(&rec, "big.jsonl", Expectations{}, Options{Limits: limits.Limits{MaxLineBytes: 2 << 20, MaxEventBytes: 4 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if r.EventCount != 1 || len(r.Events[0].Data) != maxRecordChunk+100 {
		t.Fatalf("result = %+v", r.EventCount)
	}
}

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("disk full")
	}
	f.n--
	return len(p), nil
}

func TestRecorderWriteErrors(t *testing.T) {
	if _, err := NewRecorder(strings.NewReader(""), &failWriter{}, Options{}); err == nil {
		t.Fatal("header write error not returned")
	}
	recorder, err := NewRecorder(strings.NewReader("data: a\n\n"), &failWriter{n: 1}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(recorder)
	if err != nil || string(data) != "data: a\n\n" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if err := recorder.Close(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("close = %v", err)
	}
}
