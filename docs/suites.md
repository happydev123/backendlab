# Suite files

A suite is a YAML file listing HTTP cases. Each case is one request plus the expectations checked
against the response. Suites never contain destinations or credentials: the base URL and any
credential headers come from target configuration, so the same suite can run against a local
build, staging and production.

```yaml
version: 1
name: invoices
description: Invoice API smoke checks
defaults:
  timeout: 5s
  headers:
    Accept: application/json
  compare:
    ignore: [/meta]
cases:
  - id: create-invoice
    method: POST
    path: /invoices
    body:
      json:
        customer: "c-1"
        amount: 10.10
    expect:
      status: 201
    capture:
      - name: invoice_id
        pointer: /id
  - id: get-invoice
    method: GET
    path: /invoices/{id}
    path_params:
      id: "${invoice_id}"
    expect:
      status: 200
      json:
        - {pointer: /amount, equals: 10.10}
        - {pointer: /status, matches: "^(open|paid)$"}
      max_duration: 750ms
```

## Loading rules

- The file is decoded strictly: unknown keys, duplicate keys and multiple YAML documents are errors.
- The file must not exceed `limits.max_suite_bytes` (1 MiB by default) and may hold at most
  `limits.max_cases` cases (200 by default).
- All problems found by validation are reported together.
- Durations are strings with a unit (`"750ms"`, `"5s"`); bare numbers are rejected. Durations must
  not be negative.
- Values written under `equals` and `body.json` are interpreted with JSON semantics. Numbers keep
  their literal text (`10.10` is not rounded through floating point); YAML-only forms (aliases,
  binary or timestamp tags, `0x1F`, `.inf`) are rejected, and object keys must be strings.

## Top level

| Field | Required | Description |
|---|---|---|
| `version` | yes | Format version. Must be `1`. |
| `name` | yes | Suite name, shown in reports. |
| `description` | no | Free text. |
| `defaults` | no | Settings applied to every case (below). |
| `cases` | yes | At least one case. Cases run in the order written when the suite has dependencies or writes (see Execution). |

### `defaults`

| Field | Description |
|---|---|
| `timeout` | Request timeout for cases without their own. Falls back to `limits.request_timeout` (10s by default). Must not exceed `limits.max_request_timeout`. |
| `headers` | Headers sent with every case. Case headers with the same name (case-insensitive) win. Variables are not allowed here. |
| `compare` | Comparison settings merged into every case (see `compare` below and [comparisons](comparisons.md)). |

## Cases

| Field | Required | Description |
|---|---|---|
| `id` | yes | Stable identifier: letters, digits, `.`, `_`, `-`, starting with a letter or digit, at most 64 characters, unique in the suite. Results, findings and captures are keyed by it. |
| `description` | no | Free text, shown in reports. |
| `method` | yes | Upper case: `GET`, `HEAD`, `OPTIONS`, `POST`, `PUT`, `PATCH` or `DELETE`. |
| `path` | yes | Path relative to the target base URL (below). |
| `path_params` | no | Values for `{name}` placeholders in `path`. Every placeholder needs an entry and every entry must be used. |
| `query` | no | Query parameters, name to value. |
| `headers` | no | Request headers for this case. |
| `body` | no | Request body (below). Not allowed for `GET` and `HEAD`. |
| `timeout` | no | Request timeout for this case. |
| `expect` | no | Assertions on the response (below). |
| `compare` | no | Comparison settings for compare mode. |
| `capture` | no | Values to capture from the response for later cases. |
| `stream` | no | Marks the response as a server-sent event stream (below). |

### `path`

The path must start with `/`, must not start with `//`, and must not contain a scheme, host, `?`,
`#`, whitespace, backslashes, dot segments (`.` or `..`) or `$`. Placeholders are written `{name}`
and filled from `path_params`.

The path is joined to the target base URL, keeping the base path: base `http://api.test/v1` and
path `/invoices/{id}` give `http://api.test/v1/invoices/<id>`. Literal path characters that are not
valid in a URL path (for example non-ASCII letters) are percent-encoded; existing `%XX` escapes are
kept as written.

Each path parameter value is expanded (see Substitution) and then escaped as exactly one path
segment, so a captured value `a/b c` is sent as `a%2Fb%20c` and cannot add segments. A value that
expands to an empty string, `.` or `..` is rejected for that case.

### `query`

Values are expanded and then form-encoded. Parameters are sent sorted by name, so the request is
the same on every run.

### `headers`

Header names must be valid HTTP tokens. These headers cannot be set by a suite:

- `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, `Upgrade`, `TE`, `Trailer` (controlled
  by the HTTP client);
- `Authorization`, `Proxy-Authorization`, `Cookie` (credentials come only from target
  configuration).

The request carries the target's headers (which include credentials) plus the suite's effective
headers (defaults merged with case headers). A suite header never replaces a header the target
sets. Values may contain variables in case headers; after expansion, a value containing CR, LF or
NUL is rejected for that case. Replay sets `User-Agent: backendlab/<version>` unless the target or
suite sets another value.

### `body`

Exactly one of `json` or `text`:

| Field | Description |
|---|---|
| `json` | A JSON value written in YAML. String leaves are expanded (object keys are not) and the result is encoded as JSON. Default content type `application/json`. |
| `text` | A literal string, sent as written; variables are not expanded. At most 64 KiB. Default content type `text/plain; charset=utf-8`. |
| `content_type` | Overrides the default `Content-Type`. |

The encoded body must not exceed `limits.max_request_bytes` (256 KiB by default).

### `expect`

All assertions are evaluated against each target's response separately. A failed assertion is a
finding of kind `assertion_failure` and fails the case.

| Field | Description |
|---|---|
| `status` | One status code or a list (`200` or `[200, 204]`). Codes must be 100-599. Empty accepts any status. |
| `headers` | Header name to exact expected value. Names are case-insensitive; multiple received values are joined with `", "`. |
| `json` | List of assertions on the JSON body (below). |
| `schema` | JSON Schema file, relative to the suite file, that the body must satisfy. |
| `rules` | ExtractCheck rules file, relative to the suite file, applied to the body. |
| `max_duration` | Explicit time limit for one request, measured from sending the request to reading the whole body. |

`schema` and `rules` are evaluated by the validation module. The files must exist when the run
starts, otherwise the run fails before any request is sent. When the executor has no validator
configured, the case gets an inconclusive `replay.validation_unavailable` finding instead of being
silently treated as passing.

`max_duration` is the only timing check. A single request time is an observation, not statistical
evidence of a regression: one slow request can be caused by the network or a cold cache. Timings
are always recorded in the report, but they only fail a case when they exceed an explicit
`max_duration` (`replay.max_duration_exceeded`).

#### `expect.json` assertions

Each entry has a `pointer` (RFC 6901 JSON Pointer; `""` is the whole body) and exactly one operator:

| Operator | Passes when |
|---|---|
| `equals` | The value exists and equals the given value with JSON semantics: object key order is ignored, array order matters, numbers are equal when their decimal values are equal (`10.5` equals `10.50`). |
| `exists` | `true`: the value exists (a `null` value exists). `false`: it does not. |
| `type` | The value exists and has this JSON type: `object`, `array`, `string`, `number`, `boolean` or `null`. |
| `matches` | The value is a string matching this regular expression (RE2 syntax, unanchored unless you add `^`/`$`). |

`equals: null` checks that the value is present and `null`, which is the same as `type: null`.
An absent field fails it; use `exists: false` to require absence.

If the response body is not JSON, JSON assertions fail with `replay.body_not_json`. Expected and
observed values in findings are redacted and truncated to `limits.max_value_preview`.

### `capture`

```yaml
capture:
  - name: invoice_id
    pointer: /id
```

| Field | Description |
|---|---|
| `name` | Variable name: letter or `_` first, then letters, digits or `_`, at most 64 characters. Unique across the suite. |
| `pointer` | JSON Pointer into the response body. |

The value must exist and be a string, number or boolean. Numbers keep their original text (`1.50`
stays `1.50`); booleans become `true` or `false`. Anything else (absent, null, object, array,
non-JSON body, or no response at all) is a capture failure, reported as the assertion failure
`replay.capture_failed`.

A case that references a variable whose capture failed is not sent. It gets status `skipped` with
the inconclusive finding `replay.dependency_unavailable`, which names the variable and the case
whose capture failed. Its own captures then count as failed too, so the explanation follows the
chain.

Captures are not supported on stream cases. A captured value found at a redacted path (for example
under a `token` key) is added to the run's secret list and scrubbed from everything reported
afterwards.

### `compare`

Used only by `backendlab compare`. Full semantics are in [comparisons](comparisons.md).

| Field | Description |
|---|---|
| `ignore` | JSON Pointer patterns excluded (with everything below them) before comparison. `*` matches one segment. |
| `headers` | Response headers compared by value; no header is compared unless listed. |
| `tolerance` | `{absolute: "0.01", relative: "0.05"}`: allowed numeric difference for every number. Decimal strings, not negative, at least one set. |
| `path_tolerances` | List of `{pointer, absolute, relative}`; overrides `tolerance` for numbers at or below the pointer pattern. First match wins. |
| `unordered` | List of `{pointer, key}`: arrays matched by the key member instead of position. `key` is required. |
| `skip_status` | `true` disables status code comparison. |

Defaults and case settings merge: `ignore` and `headers` concatenate, `path_tolerances` and
`unordered` concatenate with case entries first, a case `tolerance` replaces the default, and
`skip_status` is true if either sets it.

### `stream`

Marks a case whose response is a server-sent event stream. The response body is not buffered; it is
handed, bounded by `limits.max_stream_bytes`, to the stream reader, which checks these expectations.
Without a stream reader configured, the case gets the inconclusive `replay.stream_unavailable`.
`expect.status` and `expect.headers` still apply; `expect.json`, `expect.schema` and `expect.rules`
do not apply to streams and are reported as unsupported.

| Field | Description |
|---|---|
| `required_events` | Event types that must appear. |
| `terminal_event` | Event type that marks application-level completion. |
| `terminal_data` | Data payload that marks completion, such as `[DONE]`. |
| `require_terminal` | When true, a stream that ends without the terminal marker fails. Without it, EOF before the marker is only reported. Requires `terminal_event` or `terminal_data`. |
| `json_payloads` | Event data payloads must be JSON. |
| `schema` | JSON Schema file for event payloads, relative to the suite. Requires `json_payloads: true`. |
| `max_gap` | Longest allowed time between events. |
| `max_duration` | Longest allowed stream duration. |
| `adapter` | Optional payload interpretation. Only `chat-delta` is supported. |

The case timeout covers the whole stream, not only the response headers.

## Substitution

Substitution is deliberately small. `${name}` inserts a value captured by an earlier case and `$$`
writes a literal `$`. There are no expressions, defaults, functions or environment lookups; any
other use of `$` is an error. Templates are expanded only in:

- `path_params` values (then escaped as one path segment);
- `query` values (then form-encoded);
- case `headers` values (CR, LF and NUL rejected after expansion);
- string leaves of `body.json` (then JSON-encoded, so quotes and backslashes in captured values
  are escaped correctly).

Everything else is literal: the path itself, the method, `body.text`, default headers and object
keys in `body.json`. Validation checks that every referenced variable is captured by an earlier
case.

## Execution

### Write safety

Methods other than `GET`, `HEAD` and `OPTIONS` are mutating. A suite with any mutating case is
refused before any request is sent unless writes are explicitly allowed (`--allow-writes` on the
command line); the error lists the mutating case IDs.

Nothing is ever retried: not on timeouts, not on connection errors, not on 5xx responses. Each case
sends its request exactly once per target.

Cancelling a run, a request timeout or the run timeout only stops waiting. A request that already
reached the target may still be processed there, and its side effects (a created record, a sent
email, a charged card) are not undone. Run mutating suites only against targets where that is
acceptable.

### Order and concurrency

Cases run concurrently, up to `limits.max_concurrency` at a time, unless the suite has any capture,
any variable reference or any mutating case; then every case runs one at a time in suite order.
Reports always list cases in suite order. In compare mode each case runs on the baseline and then
on the candidate, and each target has its own captured variables.

### Timeouts and cancellation

- Each request has a timeout: the case `timeout`, else `defaults.timeout`, else
  `limits.request_timeout`. It covers sending the request and reading the whole response body.
- The whole run is bounded by `limits.run_timeout` (10 minutes by default).
- Transport errors are distinguished: `replay.timeout` (request timeout), `replay.run_timeout`,
  `replay.cancelled`, `replay.connection_failed`, `replay.tls_failed`,
  `replay.response_too_large`, `replay.too_many_redirects`, `replay.redirect_blocked`,
  `replay.request_invalid` (the request could not be built) and `replay.request_failed`.
- When the run is cancelled, the case in flight gets `replay.cancelled` and status `cancelled`;
  cases not yet started get status `cancelled`; the report status is `cancelled`.
- When the run timeout expires, the case in flight gets `replay.run_timeout` and status `error`;
  cases not yet started are `skipped`; the report status is `incomplete`.

### Responses

Response bodies are read up to `limits.max_response_bytes` (2 MiB by default). A larger body is the
transport error `replay.response_too_large`; it is never silently truncated. Bodies are always
closed.

### Redirects

Redirects are not followed by default; the 3xx response is the result. A target with
`follow_redirects: true` follows at most five redirects. Once a redirect leaves the origin (scheme,
host and port) of the original request, every header from the target configuration, including all
credentials, is removed from that request and every later one in the chain, even if the chain
returns to the original origin. When the caller supplies a redirect policy (server mode uses this
for egress checks), it is consulted for every hop.

### TLS

Certificates are always verified and the minimum version is TLS 1.2. A target's `ca_file` adds
certificates to the system roots; verification cannot be disabled. Proxy environment variables are
ignored.

### Case status

| Status | Meaning |
|---|---|
| `passed` | Every check passed. |
| `failed` | An assertion failed, or a comparison found a difference. |
| `error` | A transport error occurred on at least one target. |
| `inconclusive` | A check could not be evaluated (for example no validator configured, or an unordered array that could not be matched). |
| `skipped` | Not executed: a required variable was unavailable, or the run timeout expired first. |
| `cancelled` | Not executed, or interrupted, because the run was cancelled. |

### Secrets in reports

Resolved credentials are registered with the redactor for every run and scrubbed from all error
messages, previews and retained bodies. Sensitive headers and JSON keys are redacted by the
configured policy. Request headers are not written to reports; request URLs appear only inside
transport error messages, with user info and sensitive query parameters redacted.
