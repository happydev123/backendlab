# Query plan comparison (QueryLens)

`backendlab plans compare` reads two saved PostgreSQL `EXPLAIN (FORMAT JSON)` plans, a
*before* and an *after*, and reports how the second differs from the first: changed scan and
join strategies, row estimates far from the actual rows, sorts and hashes that started spilling
to disk, temporary-file I/O, nodes that appeared or disappeared, and cost, time and buffer
changes.

The comparison is offline. QueryLens never connects to a database and never runs SQL; it only
reads the files it is given. It does not recommend indexes or schema changes, and it does not
claim that a plan difference explains an HTTP or application latency regression. Both
statements are repeated as limitation lines in every report.

## Capturing plans

Run the same statement against the two environments (or before and after a change) and save
the JSON output unaligned, so that psql does not add a header or line continuation markers:

```sh
psql -At -c "EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON) SELECT ..." > before.json
psql -At -c "EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON) SELECT ..." > after.json
```

- `EXPLAIN (FORMAT JSON)` without `ANALYZE` gives an estimated plan: shape, costs and row
  estimates only.
- `ANALYZE` executes the statement. For `INSERT`, `UPDATE` or `DELETE`, run it inside a
  transaction and roll back. QueryLens does not do this for you because it never runs SQL.
- `BUFFERS` adds block counters, which temp I/O findings need. (PostgreSQL 18 includes them
  with `ANALYZE` by default.)
- `SETTINGS` records planner settings that differ from the defaults, such as `work_mem`. They
  are parsed and kept in the plan model; they are not compared.
- `TIMING OFF` is supported: rows and loops are compared, node times are reported as unknown.

## Command

```sh
backendlab plans compare before.json after.json
backendlab plans compare before.json after.json --fail-on scan,spill --format markdown
```

Exit codes follow the rest of BackendLab: `0` when nothing failed, `1` when a finding in a
`--fail-on` class was found, `2` when an input could not be read or parsed. By default nothing
fails: differences are warnings and observations are informational.

## Accepted input

QueryLens accepts exactly what PostgreSQL prints for one statement:

- the top-level JSON array holding one statement object with a `"Plan"` key, plus the optional
  `"Planning"`, `"Planning Time"`, `"Execution Time"`, `"Triggers"`, `"JIT"`, `"Settings"`,
  `"Query Identifier"` and `"Serialization"` keys; or
- that statement object on its own (`{"Plan": {...}}`).

A UTF-8 byte order mark and surrounding whitespace are ignored. Everything else is rejected
with an error naming the problem:

| Input | Error |
|---|---|
| empty file | `input is empty` |
| `[]` | `JSON array is empty; expected one EXPLAIN statement` |
| array with two or more statements | `input holds N statements; compare one statement per file` |
| two JSON documents in one file | `input holds more than one JSON document` |
| JSON without a `"Plan"` key | `not an EXPLAIN JSON plan: no "Plan" key (found keys ...)` |
| `FORMAT XML`, `FORMAT YAML`, text format | `input looks like EXPLAIN (FORMAT ...) output; only FORMAT JSON is supported` |
| psql aligned output with a `QUERY PLAN` header | `input looks like psql table output` |
| a known key with the wrong JSON type | `at /Plan/Plans/0: field "Plan Rows" must be a number, got string` |
| larger than `limits.max_plan_bytes` (default 16 MiB) | `input exceeds size limit` |
| JSON nested deeper than 216 levels | `JSON nesting is deeper than 216 levels` |
| plan nodes nested deeper than 100 | `plan nesting is deeper than 100 nodes` |
| more than 10000 plan nodes | `plan has more than 10000 nodes` |

Numbers are decoded as exact decimals (`json.Number`), never through `float64`. Negative numbers
and literals longer than 40 characters or with an exponent beyond ±40 are rejected; PostgreSQL
never prints them for plan metrics.

### Unknown versus zero

A metric that the plan does not contain is *unknown*. It is stored as a nil value and never
read as zero. An estimated plan has no actual rows, an `ANALYZE` plan without `BUFFERS` has no
block counters, and a `TIMING OFF` plan has no node times; rules that need those values either
skip the node or report an inconclusive finding. A value PostgreSQL printed as `0` is a known
zero.

### Recognized, ignored and unknown keys

The node model holds the keys listed below. Other keys that PostgreSQL emits and that the
comparison does not use (for example `Async Capable`, `Scan Direction`, `Output`, `Group Key`,
`Heap Fetches`, `Cache Hits`, `Workers`) are accepted and ignored. Any remaining key is counted
as unknown, ignored, and named (up to 20 distinct names) in a limitation line, so a plan from a
newer PostgreSQL version still parses.

Node keys held by the model: `Node Type`, `Parent Relationship`, `Subplan Name`,
`Relation Name`, `Schema`, `Alias`, `Index Name`, `CTE Name`, `Function Name`, `Join Type`,
`Strategy`, `Partial Mode`, `Parallel Aware`, `Startup Cost`, `Total Cost`, `Plan Rows`,
`Plan Width`, `Actual Startup Time`, `Actual Total Time`, `Actual Rows`, `Actual Loops`,
`Rows Removed by Filter`, `Rows Removed by Join Filter`, the shared, local and temp block
counters, the I/O timing keys of PostgreSQL 16 and 17, `Sort Key`, `Sort Method`,
`Sort Space Used`, `Sort Space Type`, `Hash Buckets`, `Hash Batches`, `Original Hash Batches`,
`Peak Memory Usage`, `HashAgg Batches`, `Disk Usage`, `Workers Planned`, `Workers Launched`,
`Filter`, `Index Cond`, `Recheck Cond`, `Hash Cond`, `Merge Cond`, `Join Filter` and `Plans`.

### Analyzed, buffers and timing

- A plan is *analyzed* when any node has `Actual Loops`.
- It has *buffers* when any node has a block counter.
- It has *timing* when any node has `Actual Total Time`.

Two plans with different flags can still be compared. When only one side is analyzed, the
report contains a `plans.runtime_inconclusive` finding and a limitation line, and only plan
shape, strategies, costs and row estimates are compared.

## Node paths and labels

Each node has a path: the JSON Pointer of the node inside the statement object, such as
`/Plan` for the root and `/Plan/Plans/0/Plans/1` for the second child of the first child.
Findings about a matched pair use the after plan's path as their location and list the before
path in the `before_path` detail. Findings about a node that exists only in the before plan use
a location prefixed with `before:`, for example `before:/Plan/Plans/1`. Statement-level
observations use `/Execution Time`.

Every finding also names the node with a short label in the style of EXPLAIN's text output, for
example `Seq Scan on invoices`, `Index Scan using invoices_pkey on invoices i`,
`Hash Left Join`, `Partial HashAggregate` or `Aggregate [InitPlan 2]`. Labels are built from
relation, index, CTE and function names only. Condition text (`Filter`, `Index Cond` and so on)
is kept in the model but never copied into findings, because it can contain literal values from
the query.

## How nodes are matched

EXPLAIN output carries no node identity. Two plans for the same statement can reorder join
inputs, insert or remove nodes, or change shape entirely, and nothing in the JSON says which
before node "is" which after node. QueryLens pairs nodes with the heuristic below. **A pair means
that two nodes play the same role in their plans; it cannot guarantee that they are the same
executor node.** Every pair records a confidence (`high`, `medium`, `low`) and a reason, and
findings about a pair repeat both in their details.

### Signatures

Each node has a *family* (`scan`, `join`, `aggregate`, `sort`, `hash`, `materialize`, `gather`,
`append`, `setop`, `limit`, `modify`, `result`, `other`) and a *signature*:

- scans: family plus the object they read: `relation <schema.name>`, `CTE <name>`,
  `function <name>`, or `alias <alias>` for subquery and values scans;
- all other nodes: family plus the sorted set of relations scanned anywhere below them, for
  example `join:{customers,invoices}`.

Index Scan, Seq Scan and Bitmap Heap Scan on the same table therefore share a signature, which is
what allows a strategy change to be detected.

### Alignment

Matching runs top-down from the roots.

1. **Roots.** Roots with the same signature are paired (`high`). Roots of the same family with
   different signatures are paired (`medium`). If one root is a *wrapper* (below) whose single
   child has the other root's signature, the child is paired with the other root and the wrapper
   is reported as added or removed (`medium`). Otherwise the roots are not paired, a
   `plans.structure_changed` finding is reported at `/Plan`, and no node-level comparison takes
   place.
2. **Children of a matched pair** are aligned in four passes, each considering only children not
   yet used:
   1. same signature and same `Parent Relationship` (`high`), taking candidates in plan order;
   2. same signature with a different `Parent Relationship`, for example join inputs that swapped
      sides (`medium`);
   3. a wrapper on one side whose single main child has the signature of a child on the other
      side (`medium`): the wrapper is reported as added or removed and its child is paired;
   4. positional fallback: the remaining children are paired by position only when they have the
      same family and the same `Parent Relationship` (`low`); the reason names both signatures.
3. **Leftovers.** Children still unpaired are reported as `plans.node_added` or
   `plans.node_removed`, once per subtree root with the number of nodes below it. When both
   sides keep unpaired children, the subtree shapes diverge: a `plans.structure_changed` finding
   is reported at the pair, listing the leftover children of each side, and matching does not
   continue into those subtrees. QueryLens stops rather than inventing pairings between nodes
   that do not correspond.
4. Paired children are aligned recursively in the same way.

*Wrappers* are single-child nodes the planner commonly inserts above another node: `Hash`,
`Sort`, `Incremental Sort`, `Materialize`, `Memoize`, `Gather` and `Gather Merge`. Init plans and
subplans attached to a wrapper do not count as its main child; when a wrapper is added or
removed, its init plans and subplans are reported as added or removed subtrees too.

### Examples

- Hash Join to Nested Loop: before `Hash Join -> [Seq Scan on invoices, Hash -> Seq Scan on
  customers]`, after `Nested Loop -> [Seq Scan on invoices, Index Scan on customers]`. The joins
  match (`join:{customers,invoices}`), the invoices scans match in pass 1, and the customers
  scans match in pass 3 through the removed `Hash`. The report shows a join strategy change, a
  scan strategy change on customers and a removed `Hash` node.
- Partition members listed in a different order match by relation in pass 1, with no added or
  removed nodes.
- A serial aggregate that became a parallel one (`Aggregate -> Seq Scan` versus
  `Finalize Aggregate -> Gather -> Partial Aggregate -> Parallel Seq Scan`) matches at the root
  only. The scan and the `Gather` subtree have different families and signatures, so the report
  shows a structure change, a removed scan and an added `Gather` subtree of three nodes instead
  of a guessed pairing.

## Findings

All findings use module `plans`.

| Code | Kind | Default severity | `--fail-on` class |
|---|---|---|---|
| `plans.scan_strategy_changed` | difference | warning | `scan` |
| `plans.join_strategy_changed` | difference | warning | `join` |
| `plans.estimate_mismatch` | difference | warning | `estimate` |
| `plans.sort_spill_introduced` | difference | warning | `spill` |
| `plans.temp_io_introduced` | difference | warning | `temp_io` |
| `plans.structure_changed` | difference | warning | `structure` |
| `plans.node_added` | difference | warning | `structure` |
| `plans.node_removed` | difference | warning | `structure` |
| `plans.cost_changed` | observation | info | |
| `plans.rows_estimate_changed` | observation | info | |
| `plans.execution_time_changed` | observation | info | |
| `plans.actual_time_changed` | observation | info | |
| `plans.buffers_changed` | observation | info | |
| `plans.loops_changed` | observation | info | |
| `plans.runtime_inconclusive` | inconclusive | info | |
| `plans.timing_inconclusive` | inconclusive | info | |
| `plans.buffers_inconclusive` | inconclusive | info | |
| `plans.spill_inconclusive` | inconclusive | info | |

### Scan strategy

Reported for a matched pair of relation scans (`Seq Scan`, `Index Scan`, `Index Only Scan`,
`Bitmap Heap Scan`, `Tid Scan`, `Tid Range Scan`, `Sample Scan`) on the same relation whose
access path differs. The access path is the operation (including `Parallel`) plus the index;
for a Bitmap Heap Scan the indexes come from the Bitmap Index Scan nodes below it. Index Scan to
Seq Scan, a different index, a switch to a bitmap or index-only scan and a switch to a parallel
scan are all reported. Scans of different relations (possible after a positional match) are
not compared.

### Join strategy

Reported for a matched pair of joins when the algorithm (`Nested Loop`, `Hash Join`,
`Merge Join`) or the join type (`Inner`, `Left`, `Semi`, `Anti`, ...) changes. For an analyzed
nested loop the `inner_loops` detail shows how often the inner side ran.

### Estimate mismatch

PostgreSQL reports `Plan Rows` as the estimate for **one execution** of a node and `Actual Rows`
as the **average per execution** over `Actual Loops` executions. QueryLens compares the per-loop
values and uses totals (per-loop value times loops) for the noise floor:

```
ratio          = max(plan_rows, actual_rows) / max(min(plan_rows, actual_rows), 1)
estimated_total = plan_rows * loops
actual_total    = actual_rows * loops
mismatch       = ratio >= row_factor  and  max(estimated_total, actual_total) >= min_rows
```

Defaults: `row_factor` 10, `min_rows` 100. Values below one row count as one row in the ratio,
so an estimate of 500 rows against 0 actual rows is a ratio of 500.

Example: the inner index scan of a nested loop with `Plan Rows` 1, `Actual Rows` 40 and
`Actual Loops` 2380 has a ratio of 40, an estimated total of 2380 rows and an actual total of
95200 rows, so it is reported. The same node with `Actual Loops` 1 has 40 rows in total, which
is below the floor, and is not reported.

The rule runs over every node of the after plan when the after plan is analyzed. Nodes that
never ran (`Actual Loops` 0) are skipped. When the node has a counterpart, the details include
`before_ratio` and whether the before node crossed the thresholds too (`before_mismatch`).
Before PostgreSQL 18, `Actual Rows` is rounded to an integer per loop. In parallel plans, both
values of a parallel-aware node are per process (workers plus leader), so they stay comparable.

### Spills

A node *spills* when it reports a sort on disk (`Sort Space Type: Disk` or a `Sort Method`
starting with `external`), a `Hash` with more than one batch, or a hashed aggregate with
`HashAgg Batches` above 1 or a positive `Disk Usage`. A node *stayed in memory* when it reports
the same keys with in-memory values. `plans.sort_spill_introduced` is reported when a matched
node spills in the after plan and stayed in memory before, or when an added node spills and the
before plan is analyzed. When the before node does not say (an estimated plan),
`plans.spill_inconclusive` is reported instead. A node that already spilled before is not
reported.

### Temporary I/O

Block counters in EXPLAIN are cumulative: a node's counters include its children's. QueryLens
attributes temp blocks (`Temp Read Blocks` plus `Temp Written Blocks`) to the node that does the
I/O by subtracting the direct children's temp blocks, clamped at zero. Without this, one
spilling sort would be reported again on every ancestor. `plans.temp_io_introduced` is reported
when a matched node's own temp blocks go from zero to a positive value or at least double, and
for added nodes with temp blocks when the before plan has buffer counters. Unknown counters
are never treated as zero.

### Observations

Observations never fail a comparison. Thresholds keep run-to-run noise out of the report.

- `plans.cost_changed`: root `Total Cost` changed. Costs are **planner cost units, not
  milliseconds**, and are only comparable between plans produced with the same cost settings.
- `plans.rows_estimate_changed`: a matched node's `Plan Rows` changed at least 2x and the larger
  value is at least `min_rows`.
- `plans.execution_time_changed`: statement `Execution Time` changed. EXPLAIN ANALYZE timing
  includes instrumentation overhead and single runs vary; repeat runs before drawing conclusions.
- `plans.actual_time_changed`: total node time (per-loop `Actual Total Time` times loops)
  changed by at least 1 ms and at least 2x (or from zero). Reported only for the root and for
  leaf nodes (plus Bitmap Heap Scans), because inner node times include their children.
- `plans.buffers_changed`: the root's cumulative block counters changed; the message lists each
  changed counter.
- `plans.loops_changed`: a matched node ran at least 2x more or fewer times. Actual rows and
  times are per-loop averages, so totals scale with loops.

### Inconclusive findings

- `plans.runtime_inconclusive`: only one plan is analyzed.
- `plans.timing_inconclusive`: only one plan has node timings, or only one has `Execution Time`.
- `plans.buffers_inconclusive`: both are analyzed but only one has block counters.
- `plans.spill_inconclusive`: a node spills after and the before node does not report its sort
  or hash memory use.

## Failing a comparison

`--fail-on` takes a comma-separated list of classes: `scan`, `join`, `estimate`, `spill`,
`temp_io`, `structure`, or `all`. Differences in the listed classes are promoted to severity
`error` (with a `fail_on` detail naming the class), which makes the report fail with exit code
1. Observations and inconclusive findings are never promoted. Unknown classes are rejected.

## Report

The report uses command `plans.compare`, lists both input files with their SHA-256 digests,
sorts findings deterministically and contains no timestamps, so the same inputs always render
the same text, JSON and Markdown. At most 1000 findings are kept by default; the number left out
is reported in `findings_dropped` and in a limitation line.

Every report includes these limitation lines:

- Node matching is heuristic; a pair may not be the same executor node.
- QueryLens does not recommend indexes or schema changes.
- A plan difference does not by itself explain an HTTP or application latency regression.

Depending on the inputs, it also states that times come from single EXPLAIN ANALYZE runs, that
one or both plans are estimated, that buffer counters are missing on one side, where plan shapes
diverged, and which unknown keys were ignored.

## Worked example

`examples/plans/before.json` and `examples/plans/after.json` hold analyzed plans (with buffers)
for the same statement:

```sql
SELECT o.id, o.total, c.name
FROM orders o JOIN customers c ON c.id = o.customer_id
WHERE o.created_at >= '2026-10-01'
ORDER BY o.total DESC;
```

In the before plan, `orders` is read with `Index Scan using orders_created_at_idx` and the final
sort uses `quicksort` in memory. In the after plan, the filter matches about ten times as many
rows, `orders` is read with a `Seq Scan` that removes 1141590 rows by filter, and the sort
becomes an `external merge` on disk with 737 temp blocks.

```sh
backendlab plans compare examples/plans/before.json examples/plans/after.json --fail-on scan,spill
```

reports, among the observations:

- `plans.scan_strategy_changed` at `/Plan/Plans/0/Plans/0`: `Index Scan using
  orders_created_at_idx` to `Seq Scan` on `orders` (error, class `scan`);
- `plans.sort_spill_introduced` at `/Plan`: the sort now spills to disk (error, class `spill`);
- `plans.temp_io_introduced` at `/Plan`: 737 temp blocks where there were none (warning);
- cost, execution time, node time and buffer observations, each with its caveat.

The exit code is 1 because of the two promoted findings. Without `--fail-on`, the same report
passes with exit code 0.
