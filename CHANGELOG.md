# Changelog

All notable changes to this project are documented here.

## [v1.0.0] — 2026-07-16

### Added

#### SQL Pipeline
- **Lexer** — full SQL tokenizer with keyword recognition, string escaping, and operator multi-char lookahead
- **Parser** — recursive-descent parser supporting all DML, DDL, CTE, subquery, and window-function syntax
- **Analyzer** — semantic analysis: table/column name resolution, scope tracking, CTE resolution, type compatibility checks
- **Logical Planner** — AST → `LogicalPlan` tree with 18 node types covering all supported SQL features
- **Physical Planner** — cost-driven `LogicalPlan → PhysicalPlan` conversion with join algorithm selection
- **Executor** — Volcano/iterator model with `Open / Next / Close` lifecycle; all physical plan nodes wired

#### Operators
- `SeqScan` — sequential heap table scan with alias support
- `Filter` — predicate evaluation (WHERE / HAVING)
- `Projection` — expression evaluation and output column shaping
- `HashJoin` — build/probe hash join; INNER and LEFT join modes
- `NestedLoopJoin` — INNER, LEFT, RIGHT, and FULL OUTER joins
- `SortMergeJoin` — sort-both-sides merge join; INNER, LEFT, and RIGHT modes
- `HashAggregate` — hash-based GROUP BY with COUNT, SUM, AVG, MIN, MAX, STDDEV, VARIANCE, and DISTINCT aggregation
- `Sort` — in-memory multi-column ORDER BY with NULLS FIRST/LAST support
- `Limit` — LIMIT with OFFSET
- `WindowOp` — window functions: ROW_NUMBER, RANK, DENSE_RANK, NTILE, LAG, LEAD, FIRST_VALUE, LAST_VALUE, NTH_VALUE, and all aggregate window functions with ROWS/RANGE frame modes
- `SetOpOp` — UNION [ALL], INTERSECT [ALL], EXCEPT [ALL]
- `DedupeOp` — SELECT DISTINCT
- `InsertOp` — INSERT INTO (VALUES and INSERT … SELECT)
- `UpdateOp` — UPDATE with WHERE
- `DeleteOp` — DELETE with WHERE
- `ExplainOp` — EXPLAIN (with optional ANALYZE)
- `CreateTableOp` — CREATE TABLE (with optional CTAS)
- `DropTableOp` — DROP TABLE [IF EXISTS]
- `AlterTableOp` — ADD/DROP/RENAME COLUMN, RENAME TABLE
- `EmptyOp` — zero-row placeholder for WHERE FALSE
- `ConstantScanOp` — single-row source for SELECT without FROM

#### Expression Evaluation (`EvalExpr`)
- All comparison operators: `=`, `!=`, `<`, `<=`, `>`, `>=`
- Arithmetic: `+`, `-`, `*`, `/`, `%`
- Boolean: `AND`, `OR`, `NOT`
- String concatenation: `||`
- `LIKE` / `NOT LIKE` with `%` and `_` wildcards (compiled and cached)
- `IS NULL` / `IS NOT NULL`
- `BETWEEN` / `NOT BETWEEN`
- `IN` / `NOT IN` (value list and subquery)
- `EXISTS` / `NOT EXISTS`
- Scalar subqueries
- Correlated subqueries (via `OuterTuple` in `ExecContext`)
- `CASE WHEN … THEN … ELSE … END`
- `CAST(expr AS type)`
- 50+ built-in functions (string, numeric, date/time, type conversion)

#### Optimizer
- **Rule-based optimizer** — fixed-point loop (up to 10 iterations) over a pipeline of rules
- **PredicatePushdown** — pushes filter conditions through projections, joins, and aggregations
- **ProjectionPushdown** — removes unreferenced columns before joins
- **ConstantFolding** — evaluates constant expressions at plan time (`3 + 4 → 7`, `x AND TRUE → x`)
- **EliminateDeadFilter** — removes `Filter(TRUE)` nodes; replaces `Filter(FALSE)` with `EmptyRelation`
- **Cost-based optimizer (CBO)** — dynamic-programming join order enumeration (n ≤ 10) using table statistics
- **Statistics collector** — row count, page count, NDV, null count, min/max, equi-depth histograms

#### API
- `POST /api/query` — execute SQL with optional EXPLAIN and stats output
- `POST /api/explain` — plan-only (no execution)
- `GET /api/schema` — catalog inspection
- `GET /api/stats` — per-column statistics
- `POST /api/schema/table` — DDL via HTTP
- `POST /api/schema/seed` — demo data reset with optional Bearer token auth
- `GET /health` — liveness endpoint
- Structured JSON logging via `log/slog`
- Graceful HTTP shutdown on SIGINT/SIGTERM
- Per-query execution timeout (30 s, configurable)
- CORS middleware with configurable origin
- Request body size limit (1 MB)

#### Infrastructure
- `Dockerfile` — multi-stage build (Go 1.23 → Alpine 3.20 runtime)
- `docker-compose.yml` — single-service compose with health check
- `.github/workflows/ci.yml` — GitHub Actions CI: `go vet` + `go test -race` on every push/PR
- `.github/ISSUE_TEMPLATE/` — bug report and feature request templates
- `.github/PULL_REQUEST_TEMPLATE.md` — PR description template
- `CONTRIBUTING.md` — project structure, operator/rule contribution guide
- `internal/executor/operators/README.md` — Volcano model and operator catalogue

### Fixed

- **BUG004** — Window function RANGE mode: `CURRENT ROW` now correctly expands to all peer rows that share the same ORDER BY key value, not just the physical row position. Previously, `SUM(val) OVER (ORDER BY category RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)` produced per-row values instead of group totals for ties.
- **Operator resource leaks** — `HashJoin`, `NestedLoopJoin`, and `SortMergeJoin` now close the left operator if the right side fails to open or errors during materialization. `WindowOp` closes its child operator on any error during `Open`.
- **DELETE silent error** — WHERE-clause expression evaluation errors in `DeleteOp` are now propagated as errors instead of being silently treated as "row does not match" (which previously caused 0-row-deleted results with no error message).
