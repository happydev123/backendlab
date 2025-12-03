# Comparisons

`backendlab compare` sends every case of a suite to two targets, a baseline and a candidate, and
reports how the candidate's response differs from the baseline's. This page describes exactly what
is compared and how each kind of difference is reported. The implementation is in
`internal/compare`; it is pure (no network) and deterministic.

## What is compared

For each case, after both targets returned a complete response:

1. **Status code**, unless the case or suite sets `compare.skip_status: true`.
2. **Headers named in `compare.headers`**, and only those. Header names are case-insensitive.
   Multiple values of one header are joined with `", "` in received order and compared as one
   string. Headers that are not listed are never compared, because many (dates, request IDs,
   server versions) differ between environments without meaning anything.
3. **The body**, as described below.

A case is not compared when either target produced a transport error (timeout, connection failure,
oversized response and so on). The transport error is reported instead, and the case status is
`error`.

Stream cases (cases with a `stream` spec) compare status and selected headers only. Their bodies
are consumed by the stream reader and checked against the stream expectations per target; the
report records this as a limitation.

## Body comparison

Each body is first parsed as a single JSON document (trailing data makes it non-JSON).

| Baseline | Candidate | Comparison |
|---|---|---|
| JSON | JSON | Structural comparison of the decoded values (below). |
| not JSON | not JSON | Size and SHA-256 digest. Equal bytes are equal; anything else is `compare.body_changed`. |
| JSON | not JSON (or the reverse) | `compare.body_format_changed`; nothing further is compared. |

Empty bodies count as non-JSON. Raw JSON text is never compared byte for byte to decide equality:
formatting, key order and number spelling do not change meaning, so two differently formatted JSON
bodies can be equal and two byte-identical non-JSON bodies are equal only as bytes. Non-JSON bodies
are described in findings by kind, size and the first 16 hex digits of their digest; their content
is never quoted.

## Structural JSON comparison

- **Objects**: key order is ignored. Members are visited in sorted key order so output is stable.
- **Absent and null are different.** A member that exists in the baseline but not in the candidate
  is `compare.field_missing`; the reverse is `compare.field_added`. When the existing side's value
  is `null`, the code is `compare.null_vs_absent` instead, because "explicitly null" and "not sent"
  are often handled differently by clients.
- **Types**: a value whose JSON type changed (for example string to number, or null to a value) is
  `compare.type_changed` and is not compared further.
- **Strings and booleans**: any change is `compare.value_changed`.
- **Arrays** keep their order by default. A length change is reported once as
  `compare.array_length_changed` at the array, and the common prefix is compared element by
  element. Extra elements are covered by the length difference and not listed individually.

### Numbers

Numbers are compared as decimals parsed from their JSON text with `shopspring/decimal`, never as
`float64`. Values with the same decimal value are equal regardless of spelling: `1`, `1.0`, `1.00`
and `1e0` are all equal. A service that encodes meaning in trailing zeros (for example a fixed
two-decimal currency rendering) is therefore not distinguished by comparison; assert the exact
string form with `expect.json` and `matches` if it matters.

Numbers longer than 400 characters or with an exponent beyond plus or minus 1000 are not converted
(decimal arithmetic on them can be very expensive). They are compared by their literal text; if
the texts differ, the result is the inconclusive `compare.number_unparsable` rather than a guess.

### Tolerances

A tolerance allows numbers to differ:

- `absolute: "0.01"` accepts `|baseline - candidate| <= 0.01`;
- `relative: "0.05"` accepts `|baseline - candidate| <= 0.05 * |baseline|`.

When both are set, a difference within either one is accepted. Tolerance values are decimal
strings and must not be negative. Note that relative tolerance is measured against the baseline,
so it is not symmetric, and a baseline of zero accepts no relative difference.

`compare.tolerance` applies to every number. `compare.path_tolerances` entries apply to numbers at
a pointer pattern or anywhere below it, and override the global tolerance; the first matching entry
wins (case entries are checked before suite defaults). Differences inside a tolerance are not
reported as differences; their count appears as one info-level `compare.within_tolerance`
observation per case.

### Exclusions

`compare.ignore` lists JSON Pointer patterns (`/meta`, `/items/*/updated_at`). A pattern excludes
the value at the matching pointer and everything below it, before comparison and before anything is
reported, so excluded values never appear in findings. `*` matches exactly one object key or array
index; `**` is not supported. Excluding the whole document is rejected by suite validation.

Suite defaults and case settings are merged: ignore lists are concatenated, a case tolerance
replaces the default tolerance, and `skip_status` is true if either sets it.

### Unordered arrays

Some arrays have no meaningful order (search hits, line items keyed by SKU). Mark them with
`compare.unordered` entries giving a pointer pattern and a key member:

```yaml
compare:
  unordered:
    - pointer: /lines
      key: sku
    - pointer: /orders/*/items
      key: id
```

The pattern must match the array's own pointer exactly. Elements are then paired by the value of
their key member and compared recursively; differences inside a matched element are reported at the
element's baseline index. An element whose key exists only in the baseline is
`compare.element_missing` (at its baseline index) and one whose key exists only in the candidate is
`compare.element_added` (at its candidate index). Key values may be strings, numbers or booleans;
numbers match by decimal value, and the string `"7"` does not match the number `7`.

Matching refuses to guess. If any element is not an object, lacks the key, has a key that is not a
string, number or boolean, or shares its key with another element of the same array, the array is
reported once as the inconclusive `compare.unordered_unmatchable` and its elements are not compared.
The one exception: arrays that are equal position by position are accepted as equal, since they
are equal in any order.

Arrays that do not match an unordered rule are always compared in order.

## Bounds

- At most `limits.max_differences` differences are reported per case (status, headers and body
  share the bound). Further differences are counted and reported as one
  `compare.differences_dropped` finding with the count.
- Every value preview is truncated to `limits.max_value_preview` bytes.
- Nesting deeper than 64 levels is not compared; the location is reported as the inconclusive
  `compare.too_deep`.

## Redaction

Previews pass through the configured redactor before they are stored:

- values at configured `redact.json_paths`, or under a sensitive key (`password`, `token`,
  `api_key` and the other defaults), are shown as `[REDACTED]`; the message for a changed number at
  such a path also omits the size of the change;
- registered secrets (resolved target credentials) are scrubbed from every string;
- sensitive headers (`Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key` and others) are compared
  by their real values but shown as `[REDACTED]`.

Redaction is driven by configuration. A sensitive value under an unexpected key is shown unless the
policy names it.

## Findings

Each difference becomes one finding:

| Field | Value |
|---|---|
| `module` | `compare` |
| `kind` | `difference` (severity `error`), or `inconclusive` (severity `warning`) for parts that could not be compared |
| `location` | JSON Pointer into the body, `body` for the body as a whole, `status`, or `header:<Name>` |
| `expected` | baseline preview; `(absent)` when the value does not exist there |
| `observed` | candidate preview; `(absent)` when the value does not exist there |
| `details` | `baseline_target` and `candidate_target` names |

Codes:

| Code | Meaning |
|---|---|
| `compare.status_changed` | Status codes differ. |
| `compare.header_missing` / `compare.header_added` / `compare.header_changed` | A listed header is absent in the candidate, new in the candidate, or has a different value. |
| `compare.body_format_changed` | One body is JSON and the other is not. |
| `compare.body_changed` | Non-JSON bodies differ in size or digest. |
| `compare.field_missing` / `compare.field_added` | An object member exists on one side only. |
| `compare.null_vs_absent` | A member is `null` on one side and absent on the other. |
| `compare.type_changed` | The JSON type changed. |
| `compare.value_changed` | A string, boolean or number changed (numbers outside tolerance). |
| `compare.array_length_changed` | An ordered array changed length. |
| `compare.element_missing` / `compare.element_added` | A keyed element of an unordered array exists on one side only. |
| `compare.unordered_unmatchable` | Inconclusive: an unordered array could not be matched by its key. |
| `compare.number_unparsable` | Inconclusive: numbers outside the decimal bounds differ in text. |
| `compare.too_deep` | Inconclusive: nesting beyond 64 levels. |
| `compare.differences_dropped` | More differences than `max_differences`; carries the count. |
| `compare.within_tolerance` | Info observation: count of numeric differences accepted by tolerances. |

A difference fails the case (`failed`). Inconclusive findings alone make the case `inconclusive`.

## Example

Baseline body:

```json
{"id": "inv-7", "total": 100.00, "currency": "USD", "discount": 0,
 "lines": [{"sku": "a", "qty": 1}, {"sku": "b", "qty": 3}], "meta": {"request_id": "r1"}}
```

Candidate body:

```json
{"id": "inv-7", "total": 100.004, "currency": "EUR", "status": null,
 "lines": [{"sku": "b", "qty": 2}, {"sku": "a", "qty": 1}, {"sku": "c", "qty": 5}],
 "meta": {"request_id": "r2"}}
```

With `ignore: [/meta]`, `tolerance: {absolute: "0.01"}` and `unordered: [{pointer: /lines, key: sku}]`
the comparison reports `/currency` changed, `/discount` missing, `/lines/1/qty` changed (baseline
index of `sku: b`), `/lines/2` added (candidate index of `sku: c`) and `/status` null versus
absent. The total is within tolerance and is only counted; `/meta` is never looked at.
