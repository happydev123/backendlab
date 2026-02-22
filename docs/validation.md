# Structured extraction validation (ExtractCheck)

ExtractCheck validates JSON that another system has already extracted from documents such as
invoices, receipts or purchase orders. It does not read PDFs or images, performs no OCR and calls no
extraction service. The input is structured JSON; the output is a BackendLab report with one case
per document.

Each document is checked against:

- an optional JSON Schema (structure and types), and
- a declarative rules file (presence, allowed values, decimal formats, arithmetic relationships,
  dates and batch-level uniqueness).

The rules file has no expressions and runs no code. Every rule type is a fixed check described
below.

The implementation is the `internal/validate` package. The `backendlab validate` command and the
suite assertions for response bodies call into it.

## What a passing result means

Passing validation shows that the documents are consistent with the configured schema and rules. It
does not establish that a document is authentic, that its figures are factually correct, or that
the extraction matched the source document. Every validation report carries this limitation.

## Example

`examples/invoice/` contains a schema, a rules file and a batch of four invoices:

| File | Content |
|---|---|
| `invoice.schema.json` | Required invoice fields and types, line items under `$defs` |
| `invoice-rules.yaml` | Formats, line amounts, subtotal, tax, total, dates, unique invoice numbers |
| `invoices-batch.json` | `INV-1001` (valid), `INV-1002` (total off by 0.04), `INV-1003` (0.10 + 0.20 = 0.30, passes), and a second `INV-1001` |

Validating the batch yields three failing findings:

```text
Cases: 4 total, 1 passed, 3 failed
  FAIL  INV-1001
  FAIL  INV-1002
  PASS  INV-1003
  FAIL  INV-1001#3

  [error] assertion_failure validate.sum case=INV-1002 at /total
      rule "total": computed 179.96 from /subtotal (149.97) + /tax (29.99); stated /total is 180.00; difference 0.04 exceeds tolerance 0.01
      expected: /subtotal + /tax - /discount (optional) = /total (tolerance 0.01)
      observed: 180.00
      rule_id: total
```

The full text, JSON and Markdown renderings are kept as golden files under
`internal/validate/testdata/golden/`.

## Rules file

```yaml
version: 1                       # required; the only supported version
schema: invoice.schema.json      # optional, relative to the rules file
document_id: /invoice_number     # optional pointer that names each document
rules:
  - id: total                    # required, unique, [A-Za-z0-9][A-Za-z0-9_.-]*, at most 64 bytes
    type: sum                    # required, one of the types below
    description: Grand total.    # optional
    severity: error              # optional: error (default) or warning
    terms:
      - field: /subtotal
      - field: /tax
      - field: /discount
        sign: -1
        optional: true
    equals: /total
    tolerance: "0.01"
```

Loading is strict:

- unknown keys anywhere, duplicate keys and multiple YAML documents are errors;
- a key that belongs to another rule type (for example `tolerance` on a `date` rule) is an error;
- unknown rule types, missing or duplicate rule IDs, and invalid JSON Pointers are errors;
- decimals in the rules file (`tolerance`, `min`, `max`) use the decimal string grammar below, so
  `1e-2` is rejected. Unquoted YAML numbers such as `tolerance: 0.01` are read as their literal
  text and accepted.

Bounds: 1 MiB per rules file, 500 rules, 256 fields per list, 1000 allowed values, 1024-byte
patterns, 8 product factors.

### Fields and pointers

Fields are RFC 6901 JSON Pointers (`/line_items/0/amount`, `~1` for `/`, `~0` for `~`). Rules that
check the format of values (`required`, `allowed_values`, `pattern`, `decimal`, `date`) accept `*`
as a segment that matches any one array index or object key: `/line_items/*/amount`. A wildcard
pattern that matches more than 10000 values in one document makes the rule inconclusive
(`validate.too_many_matches`) rather than truncating.

Arithmetic, ordering and uniqueness rules take exact pointers. Wildcards are rejected there because
an operand must be unambiguous; `line_items_sum` and `line_item_product` cover arrays.

### Absent, null and invalid values

| Rule kind | Absent or null field | Value of the wrong type or format |
|---|---|---|
| `required` | failure (null passes only with `allow_null: true`) | not checked |
| `allowed_values`, `pattern`, `decimal`, `date` | skipped | failure |
| `sum`, `product`, `line_items_sum`, `line_item_product`, `date_order` | inconclusive `validate.input_missing` (optional sum terms contribute nothing) | failure |
| `unique` | not compared | compared by type and value |

Use a `required` rule for every field that must be present. An inconclusive finding has warning
severity: it marks the document's case as inconclusive and does not by itself fail the run.

## Rule types

### `required`

```yaml
- id: required-fields
  type: required
  fields: [/invoice_number, /total, /line_items/*/amount]
  allow_null: false              # default
```

A field holding JSON `null` is present but null; it fails unless `allow_null: true`. An empty string
is present. A wildcard pattern requires the last member in every element that exists
(`/line_items/*/amount` checks each line item); it does not require `/line_items` itself, so list
the array separately when it must exist. The last segment of a wildcard pattern must be literal.

### `allowed_values`

```yaml
- id: currency-code
  type: allowed_values
  field: /currency
  values: [EUR, GBP, USD]
```

The value must be a string equal to one of `values` (exact, case-sensitive). Non-string values fail.

### `pattern`

```yaml
- id: invoice-number-format
  type: pattern
  field: /invoice_number
  pattern: '^INV-[0-9]{4}$'
```

Go RE2 syntax (no backreferences or lookaround), which matches in linear time. Matching is
unanchored unless the pattern uses `^` and `$`. Non-string values fail.

### `decimal`

```yaml
- id: money-format
  type: decimal
  fields: [/subtotal, /total, /line_items/*/amount]
  accept: [number, string]       # default: both
  max_scale: 2                   # optional, 0..40
  min: "0"                       # optional, inclusive
  max: "1000000"                 # optional, inclusive
```

`accept` selects the JSON representations allowed: `number` (a JSON number, kept as `json.Number`
during decoding) and `string` (a decimal string). Scale is the number of fractional digits as
written, so `"10.500"` has scale 3 and fails `max_scale: 2` even though it equals 10.5.

### `sum`

```yaml
- id: total
  type: sum
  terms:
    - field: /subtotal
    - field: /tax
    - field: /discount
      sign: -1                   # +1 (default) or -1
      optional: true             # absent or null contributes nothing
  equals: /total
  tolerance: "0.01"              # default 0: exact equality
  round: {scale: 2, mode: half_up}   # optional
```

This describes `subtotal + tax - discount = total` without building any particular invoice formula
into the code. Other layouts (shipping, deposits, several tax lines) are expressed with more terms.
An optional term that is present but not a decimal still fails.

### `line_items_sum`

```yaml
- id: subtotal-from-lines
  type: line_items_sum
  items: /line_items             # array
  amount: amount                 # member name inside each element
  equals: /subtotal
  tolerance: "0"                 # optional
  round: {scale: 2, mode: half_even}  # optional
```

An empty array sums to 0. An element without the member (or with null) makes the rule inconclusive;
`items` that is not an array fails.

### `line_item_product`

```yaml
- id: line-amounts
  type: line_item_product
  items: /line_items
  quantity: quantity
  unit_price: unit_price
  amount: amount
  round: {scale: 2, mode: half_up}
  tolerance: "0"                 # optional
```

Each element is judged separately: `quantity * unit_price`, rounded if configured, must equal
`amount`. Findings point at the element's amount, for example `/line_items/1/amount`.

### `product`

```yaml
- id: tax-from-rate
  type: product
  factors: [/subtotal, /tax_rate]   # 2..8 exact pointers
  equals: /tax
  round: {scale: 2, mode: half_even}
```

Products usually carry more fractional digits than the stated value (100.00 * 0.0825 = 8.250000),
which does not matter for equality because 8.250000 equals 8.25. Configure `round` whenever the
stated value was rounded when the document was produced.

### `date`

```yaml
- id: dates
  type: date
  fields: [/issue_date, /due_date]
  layout: date                   # date (default), rfc3339, or a Go layout
```

`date` is `2006-01-02`; `rfc3339` is Go's `time.RFC3339` (offsets required, fractional seconds
accepted). A custom layout uses Go's reference time, for example `02.01.2006` or `Jan 2, 2006`.
Layouts are checked when the rules load: `DD.MM.YYYY` (no Go reference elements) and layouts
without a year, month and day are rejected. Non-string values and strings that do not parse fail;
`2024-02-30` fails because the day does not exist.

### `date_order`

```yaml
- id: due-after-issue
  type: date_order
  before: /issue_date
  after: /due_date
  layout: date                   # default date
  allow_equal: true              # default false: strictly before
```

Date-only layouts parse to midnight UTC. Layouts with offsets compare instants, so
`2024-03-01T10:00:00+02:00` is before `2024-03-01T09:00:00Z`.

### `unique`

```yaml
- id: unique-invoice-number
  type: unique
  field: /invoice_number
```

Runs across a batch only (`Validator.Batch` and `Validator.Run`); validating a single document, as
replay does for response bodies, skips it. Values compare by JSON type and value: strings exactly,
numbers by decimal value (`1.0` equals `1`), objects by content. The string `"1001"` and the number
`1001` are different. Every document of a duplicate group gets a finding naming the others, since
the data cannot tell which one is wrong.

## Decimal arithmetic

All arithmetic and comparisons use `github.com/shopspring/decimal`. Values never pass through
`float64`, so `0.1 + 0.2` equals `0.3` exactly.

Accepted inputs in documents:

- **JSON numbers**, kept as `json.Number` by the decoder, using the JSON number grammar including
  exponents (`1.5e2`).
- **Decimal strings**: an optional `-`, an integer part without leading zeros, and an optional
  fraction (`"12"`, `"-0.50"`, `"1250.00"`). Exponents, `+`, whitespace, thousands separators and
  currency symbols are rejected, because guessing what `"1,250"` means is not validation.

Values with more than 40 significant digits or a decimal exponent beyond 40 in either direction are
rejected. The bound keeps arithmetic cheap: without it `1e999999999 + 1` would build a
billion-digit integer.

Arithmetic rules accept both representations; restrict them with a `decimal` rule when the format
matters.

### Comparison

For `sum`, `product`, `line_items_sum` and `line_item_product`:

1. The computed value is calculated exactly.
2. If `round` is configured, the computed value (and only the computed value, never the stated one)
   is rounded to `scale` fractional digits:
   - `half_up`: ties away from zero (`2.345` -> `2.35`, `-2.345` -> `-2.35`);
   - `half_even`: ties to the even neighbour (`2.345` -> `2.34`, `2.355` -> `2.36`);
   - `down`: toward zero (`2.349` -> `2.34`, `-2.349` -> `-2.34`).
   Both `scale` (0..18) and `mode` are required.
3. The rule passes when `|computed - stated| <= tolerance`. The default tolerance is 0, which means
   exact decimal equality. Scale does not affect equality: `10.5` equals `10.50`. Negative
   tolerances are rejected when the rules load.

Without `round` and `tolerance` nothing is rounded or tolerated.

## Documents

A documents file may hold:

- one JSON object (one document);
- a JSON array whose elements are all objects;
- JSON Lines: one object per line, blank lines skipped, CRLF accepted.

The file is bounded by `limits.max_document_bytes` (default 4 MiB) and at most 100000 documents.
Trailing data after a JSON value, non-object documents and empty input are errors with the
position (array index or line number). A UTF-8 byte order mark is ignored.

### Document IDs

Each document becomes a report case. Its ID is the value at `document_id` when that is a non-empty
string or a number; otherwise the zero-based batch position, `#3`. A value at a redacted path is not
used. IDs longer than 128 bytes are shortened. When two documents share an ID, the later one gets its
position appended (`INV-1001`, then `INV-1001#3`), so cases stay distinct while the `unique` rule
reports the duplication.

## Findings

| Field | Content |
|---|---|
| `module` | `validate` |
| `code` | `validate.<rule type>`, or `validate.schema`, `validate.input_missing`, `validate.too_many_matches`, `validate.findings_truncated` |
| `kind` | `assertion_failure`; `inconclusive` for missing inputs and too many matches; `observation` for truncation |
| `severity` | `error` (or the rule's `severity: warning`); inconclusive findings are warnings |
| `case_id` | document ID, or the replay case ID when used as a response assertion |
| `location` | JSON Pointer of the field (the schema instance location for schema findings) |
| `expected` | the condition, for example `/subtotal + /tax - /discount (optional) = /total (tolerance 0.01)` |
| `observed` | the sanitized observed value |
| `message` | explanation; arithmetic rules include the computed value, each operand, the stated value and the difference |
| `details` | `rule_id` (and `rule_type` for inconclusive findings); `keyword` and `schema` for schema findings |

A case fails when it has an error-severity finding, is inconclusive when it has an inconclusive
finding but no failing one, and passes otherwise.

The number of findings per call is bounded by `limits.max_findings`. When the bound is reached in a
batch, each document with findings keeps its most severe one before the remaining slots are filled,
and the report records how many were dropped.

## Redaction

Observed values pass through the configured redactor before they enter a finding:

- a value at a redacted path (configured `json_paths`, or any segment that is a sensitive key such
  as `password` or `token`) is shown as `[REDACTED]`;
- nested sensitive keys inside an observed object are replaced, and registered secrets are scrubbed
  from strings;
- arithmetic messages hide the computed value and the difference when any operand is redacted,
  since they would reveal it;
- schema messages at redacted locations keep only the failing keyword, because the schema library
  may quote the value.

Observed values are truncated to `limits.max_value_preview` bytes (default 160).

## Schema

The rules file's `schema` is resolved relative to the rules file. A schema can also be supplied
separately; when both are present a document is checked against both. Schemas are single local
files: internal references (`#/$defs/...`) work; remote and cross-file references fail to compile.

In server mode (`Options.ConfineFiles`), the schema reference must be relative and must stay inside
the rules file's directory, both as written and after symbolic links are resolved.

## Package API

```go
rules, err := validate.LoadRules("examples/invoice/invoice-rules.yaml")
v, err := validate.New(rules, nil, validate.Options{Redactor: red})
docs, input, err := validate.LoadDocuments("examples/invoice/invoices-batch.json", lim)
res := v.Run(docs)                       // IDs assigned, findings sorted and bounded
rulesInput, _ := rules.Input()
rep := validate.BuildReport([]report.Input{rulesInput, input}, res.Documents, res.Findings, res.Dropped)

fs := v.Document(caseID, body)           // one document, no batch rules (replay assertions)
```

`ParseRules(data, baseDir)` parses rules held in memory; `ParseDocuments(data)` parses documents
held in memory. A `Validator` is immutable and safe for concurrent use.

## Limitations

- Validation checks consistency with the configured rules, not authenticity or correctness.
- Rule types are fixed. Relationships that need conditions ("if currency is EUR then...") or
  arbitrary formulas are not expressible by design.
- Allowed values and patterns compare strings exactly; there is no case folding or whitespace
  normalization.
- Dates without a zone are treated as UTC.
- JSON Schema support is that of `santhosh-tekuri/jsonschema/v6` with external references disabled.
