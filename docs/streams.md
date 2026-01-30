# StreamProbe: server-sent event streams

StreamProbe reads server-sent event (SSE) streams, checks them against the
expectations in a suite case, and records timing when the input carries it.
It works on live HTTP response bodies and on saved files. The code lives in
`internal/stream`.

Parsing is provider-neutral. It follows the "event stream" interpretation
rules of the WHATWG HTML standard and knows nothing about any particular
service. Interpreting payloads as incremental chat text is an optional
adapter (`chat-delta`) layered on top; it never changes how events are
framed.

## Parsing rules

The parser (`stream.NewParser`, `Feed`, `Finish`) accepts chunks of any size
in arrival order. Chunk boundaries may fall anywhere, including:

- inside a line or a field name,
- between the CR and the LF of a CRLF pair (a CR at the end of one chunk
  followed by an LF at the start of the next is one line ending),
- inside the leading byte order mark,
- inside a multibyte UTF-8 character.

The result never depends on how the input was chunked. The tests feed a
sample stream byte by byte and split at every position and every pair of
positions, and a fuzz test compares whole-buffer, byte-by-byte and randomly
chunked parses of arbitrary input.

Rules applied:

| Input | Handling |
| --- | --- |
| LF, CR, CRLF | Each is one line ending; they may be mixed. |
| Leading `EF BB BF` | One byte order mark at the very start is removed. A second one is part of the first line. |
| Line starting with `:` | Comment. Counted, otherwise ignored. |
| `name:value` | One space directly after the first `:` is removed. |
| Line without `:` | Field name with an empty value (`data` alone appends an empty line). |
| `event` | Sets the type of the event being built. The type defaults to `message`. |
| `data` | Appends the value plus a line feed; the final line feed is removed at dispatch. |
| `id` | Sets the last event ID, which persists across events. A value containing NUL is ignored (warning `stream.id_contains_nul`). |
| `retry` | Accepted only when it is ASCII digits; otherwise ignored (warning `stream.invalid_retry`). |
| Other field names | Ignored and counted (`stream.unknown_fields`, info). Field names are case-sensitive. |
| Blank line | Dispatches the event. An event whose data buffer is empty is not dispatched, and the type is reset either way. |
| End of input | Data after the last blank line is discarded, as the standard requires. If anything was pending, StreamProbe reports `stream.incomplete_event` (warning) instead of dispatching it. |

One deliberate deviation: a `retry` value longer than 9 digits is valid per
the standard but is reported as invalid here, which keeps the parsed value
inside the integer range on every platform.

`Event.ID` is the last event ID at dispatch time, so an event without its
own `id` field carries the ID of an earlier one. `Event.Retry` is set only
when a valid `retry` field appeared in that event's own block.

## Bounds

Bounds come from the shared limits configuration. Exceeding one stops
parsing; nothing is truncated silently and nothing panics.

| Limit | Applies to | Finding |
| --- | --- | --- |
| `max_line_bytes` | One line, terminator excluded. Checked before buffering, so an endless line without a terminator is caught too. | `stream.line_too_long` |
| `max_event_bytes` | All lines between two blank lines, comments included. | `stream.event_too_large` |
| `max_stream_bytes` | All bytes accepted, including the byte order mark. | `stream.stream_too_large` |
| `max_stream_events` | Dispatched events. | `stream.too_many_events` |
| `max_findings` | Findings kept in a result; the rest are counted as dropped. | (report field `findings_dropped`) |
| `max_value_preview` | Length of stream data quoted in findings. | |

Limit findings have kind `inconclusive` and severity `error`: the stream
could not be analyzed to its end, and the run fails. Events dispatched
before the stop are kept and checked.

A completed line that is not valid UTF-8 stops parsing with termination
`invalid_data` and the finding `stream.invalid_utf8` (kind
`assertion_failure`). Validation happens per completed line, so a character
split across chunks is joined before it is checked. Parsing stops rather
than replacing bytes, because a replacement would change payloads that
later checks look at.

## Termination

Every result has exactly one termination:

| Termination | Meaning |
| --- | --- |
| `terminal_event` | The configured application marker arrived: an event of type `terminal_event`, or an event whose data equals `terminal_data` exactly (for example `[DONE]`). |
| `eof` | The body ended without the marker. This is the normal end of an SSE response and is not treated as malformed. |
| `timeout` | The context deadline expired (`stream.timeout`, transport error) or `max_duration` elapsed (`stream.max_duration_exceeded`, assertion failure). |
| `cancelled` | The caller cancelled the context. Reports built from such a result have status `cancelled`. |
| `invalid_data` | A line was not valid UTF-8. |
| `limit_exceeded` | A bound in the table above was exceeded. |
| `read_error` | A body read failed for another reason (`stream.read_error`, transport error). |

When a terminal marker is configured but the stream reaches EOF without it:

- without `require_terminal`, StreamProbe records `stream.eof_before_terminal`
  as an info observation, and the run does not fail;
- with `require_terminal`, it records `stream.missing_terminal` as an
  assertion failure (severity error).

If the stream stopped abnormally (timeout, limit, read error) and
`require_terminal` is set, `stream.missing_terminal` is added as an
inconclusive warning; the termination finding already fails the run.

Events that arrive after the first terminal marker produce one
`stream.event_after_terminal` warning located at the first such event.

## Expectations

A suite case enables StreamProbe with a `stream` block:

```yaml
cases:
  - id: chat-stream
    method: POST
    path: /v1/chat
    body:
      json: {"stream": true}
    stream:
      required_events: [start, delta]
      terminal_data: "[DONE]"
      require_terminal: true
      json_payloads: true
      schema: schemas/chunk.json
      max_gap: 2s
      max_duration: 30s
      adapter: chat-delta
```

`stream.ExpectationsFromSpec` converts the block. The schema path is
resolved relative to the suite file and compiled once; it must be a single
local JSON Schema file.

| Field | Check | Finding |
| --- | --- | --- |
| `required_events` | Each listed type appears at least once. | `stream.missing_required_event` (error), location `type:<name>` |
| `terminal_event`, `terminal_data` | Identify the completion marker. | see Termination |
| `require_terminal` | EOF before the marker fails. | `stream.missing_terminal` |
| `json_payloads` | Every event's data is exactly one JSON value. | `stream.invalid_json_payload` (error), location `event:<index>` |
| `schema` | Every event's data validates. | `stream.schema_violation` (error), location `event:<index>` followed by the instance pointer, for example `event:3/choices/0` |
| `max_gap` | No gap between consecutive events is longer. | `stream.max_gap_exceeded` (error), located at the event that ended the longest gap |
| `max_duration` | The stream ends within this time from the start. | `stream.max_duration_exceeded` (error) |
| `adapter` | Optional payload interpretation. | see below |

Terminal marker events are framing, not payload: they are exempt from the
JSON, schema and adapter checks. This covers both a `[DONE]` data payload
and an event of the terminal type.

`max_gap` and `max_duration` need timing. For input without timing (saved
SSE text) they are neither passed nor failed: StreamProbe reports
`stream.timing_unavailable` as an inconclusive warning.

## Timing

Live reads and timed recordings record:

- `FirstEvent`: time from the start to the first dispatched event,
- `Duration`: time from the start to the end of reading,
- `Gaps`: the number of gaps between consecutive events, the largest gap
  (and the event that ended it), and the median gap using the nearest-rank
  method.

Times are taken when a body read returns (or, for a recording, from the
chunk's offset). An event is timed by the chunk that completed it. Events
delivered in the same read share one time, so their gap is zero; the
resolution is one read, not one event. Gaps are measured between received
events only; a stream that stalls before its first event or after its last
is caught by `max_duration` or the context deadline, not by `max_gap`.

Missing timing is reported as missing. Plain SSE text has no timing at all:
every timing field is nil, and the report carries the limitation "Saved SSE
text has no reliable timing ... none is inferred from position in the file."

## Live reading

```go
res := stream.ReadLive(ctx, resp.Body, exp, stream.Options{
    Limits:   lim,
    CaseID:   c.ID,
    Start:    requestSent,
    Redactor: red,
})
resp.Body.Close()
```

`ReadLive` stops at EOF, at the terminal marker, at a parser stop, or when
`ctx` is done or `max_duration` elapses. At the terminal marker it parses
the rest of the chunk that carried it (so events after the marker in that
chunk are reported) and then returns without waiting for more data. A server
that keeps the connection open after completion is therefore not reported
as a timeout.

`Options.Now` supplies the clock (default `time.Now`); tests inject a fake
clock instead of sleeping. It must be safe for concurrent use.

Goroutine ownership: a blocked `Read` cannot be interrupted portably, so
`ReadLive` performs body reads in one goroutine that it owns and that reads
only when asked. When `ReadLive` stops for any reason other than the
context or `max_duration`, no read is outstanding and the goroutine has
exited before `ReadLive` returns. When the context ends while a read is
outstanding, `ReadLive` returns at once and the goroutine remains inside
that single `Read` call; it exits as soon as the read returns and touches
nothing but the body and the clock. The caller must close the body (or
cancel the request context) after `ReadLive` returns, which unblocks the
read. `ReadLive` never closes the body itself.

A reader that keeps returning no data and no error is stopped after 100
such reads with termination `read_error`.

## Saved streams

`stream.InspectFile` and `stream.InspectReader` read a saved stream to its
end (a terminal marker does not stop inspection) and detect the format from
the content: input whose first non-whitespace byte is `{` must be a timed
recording, anything else is plain SSE text. An error is returned only when
the file cannot be read or a recording is malformed; problems with the
stream itself are findings.

### Timed recording format

A recording is a JSON Lines file. The first line identifies the format:

```json
{"format":"backendlab.sse-recording/v1"}
```

Each further line is one record:

```json
{"offset_ms":120.5,"chunk":"event: start\ndata: {}\n\n"}
{"offset_ms":180,"chunk_b64":"ZGF0YTogaMM="}
{"offset_ms":195.25,"chunk_b64":"qWxsbwoK"}
{"offset_ms":960,"end":"eof"}
```

The two base64 records hold `data: h` plus the first byte of `é`, then the
second byte of `é` plus `llo` and a blank line: the read boundary fell
inside the character, so neither part is valid UTF-8 on its own.

- `offset_ms` is milliseconds since the start of the request (microsecond
  precision when written by the recorder). Offsets never decrease and are at
  most 24 hours.
- A chunk record has exactly one of `chunk` (the received bytes, when they
  are valid UTF-8) or `chunk_b64` (standard base64 of the raw bytes, used
  when a read ended inside a multibyte character or the data is not UTF-8).
  Reads longer than 1 MiB are split into several records with the same
  offset.
- The optional last record has `end` set to `eof` (the body ended),
  `read_error` (a body read failed), `limit_exceeded` (the recorder reached
  `max_stream_bytes`) or `stopped` (the reader stopped before the body
  ended). A `stopped` record may name the live termination, for example
  `"termination":"timeout"`.
- Blank lines are ignored. Unknown fields, data after the end record, and
  lines longer than 8 MiB are errors.

Inspection maps the end record as follows. The terminal marker and parser
stops are recomputed from the recorded bytes, so they classify the same way
as the live read did.

| End record | Termination |
| --- | --- |
| `eof` | `eof` (or `terminal_event` when the marker was seen) |
| `read_error` | `read_error` |
| `limit_exceeded` | `limit_exceeded`, with `stream.recording_truncated` |
| `stopped` | `terminal_event` when the marker was seen; otherwise the recorded `timeout` or `read_error`; otherwise `cancelled`, with a limitation noting that later data was not captured |
| none | `eof`, with a limitation noting that the recording may have been cut off |

The end record's offset is the duration and is checked against
`max_duration`, so inspecting a recording with the same expectations gives
the same timing findings as the live read.

### Recording a live stream

`stream.NewRecorder(body, w, opts)` writes the header and returns a reader
that passes every read through unchanged while writing records to `w`.
Use it as the body given to `ReadLive`, then call `Finish(res.Termination)`
(or `Close`) to write the end record when the body did not end on its own.
A failure to write the recording never changes what the reader sees; it is
returned by `Finish`/`Close`. The recorder neither closes the body nor `w`.

## The chat-delta adapter

With `adapter: chat-delta`, each non-terminal payload is decoded and matched
against three common shapes:

```json
{"choices":[{"delta":{"content":"Hel"},"finish_reason":null}]}
{"delta":"Hel"}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}
```

The adapter assembles text (bounded by `max_response_bytes`), counts delta
events, and collects distinct finish reasons (`finish_reason` in a choice,
or `stop_reason` in a typed delta). Only the first element of `choices` is
used. Objects with a `type` that does not end in `delta` are counted as
lifecycle events. Anything else counts as unrecognized and produces
`stream.adapter_unrecognized`; a stream with no delta at all produces
`stream.adapter_no_deltas`. Both are inconclusive info findings. The adapter
never fails a run, and the parser does not depend on it.

## Redaction

Stream data quoted in findings (payload previews, ignored field values,
assembled adapter text, finish reasons) passes through the configured
redactor first and is then truncated to `max_value_preview` bytes on a
UTF-8 boundary. Redacting before truncating keeps a secret that crosses the
cut from leaving a visible prefix. Read error messages go through the
redactor's error scrubbing. `Result.Events` holds raw event data for
display by the caller, which must redact it before printing.

## Reports

`Result.Report(input)` builds a finalized report with command
`stream.inspect`: one case (the case ID, or `stream`), one observation
(bytes, events, first-event time and duration when known, and the
termination when it was abnormal), all findings, and the limitations. Two
info observations are always added: `stream.summary` (termination, counts,
format and timing details) and, when an adapter ran, `stream.adapter_summary`
(counts, finish reasons and a redacted text preview). The report contains no
wall-clock timestamps, so the same input always produces the same document.

## Limitations

- Timing resolution is one body read; network buffering can merge events
  that the server sent separately.
- Gaps are not enforced while waiting: a stall is detected when the next
  event arrives, by `max_duration`, or by the context deadline.
- Saved SSE text carries no timing, and none is inferred.
- Reconnection (`retry`, `Last-Event-ID`) is recorded but not performed;
  StreamProbe reads one response.
- The `chat-delta` adapter recognizes common payload shapes only. Payloads
  of other shapes are reported as unrecognized rather than interpreted.
