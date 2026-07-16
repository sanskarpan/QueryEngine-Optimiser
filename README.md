# QueryEngine Optimizer

A from-scratch SQL query engine with a rule-based optimizer (RBO), cost-based optimizer (CBO), and an interactive web frontend for exploring query execution plans.

## Architecture

```
web/              React + Monaco + React Flow frontend
cmd/server/       HTTP server entry point
api/              REST API handlers + DTOs
internal/
  lexer/          SQL tokenizer
  parser/         Recursive-descent parser
  ast/            AST node types
  analyzer/       Semantic analysis + scope resolution
  catalog/        Table/column metadata + Value types
  storage/        In-memory heap table storage
  planner/
    logical/      Logical plan builder
    physical/     Physical plan builder (cost-driven)
  optimizer/
    rule/         Rule-based optimizer (predicate pushdown, constant folding, …)
    cost/         Cost model, cardinality estimator, DP join-order optimizer
  stats/          Statistics collector (NDV, histograms)
  executor/       Volcano-model operator tree executor
```

### Query Pipeline

```
SQL string
  → Lexer → Parser → AST
  → Analyzer (scope resolution, type checking)
  → Logical Planner → LogicalPlan tree
  → Rule-Based Optimizer (fixed-point loop, up to 10 iterations)
  → CBO Join-Order Optimizer (DP, bitmask enumeration, n ≤ 10)
  → Physical Planner (cost-based join selection: HashJoin vs NLJoin)
  → Executor (Volcano model: SeqScan, Filter, HashJoin, HashAgg, Sort, Limit, …)
  → Result rows
```

### Optimization Rules

| Rule | Effect |
|------|--------|
| PredicatePushdown | Moves filters closer to scans, through joins and projections |
| ProjectionPushdown | Drops unreferenced columns below joins |
| ConstantFolding | Evaluates `3 > 2 → TRUE`, `x AND TRUE → x`, etc. |
| EliminateDeadFilter | Removes `Filter(TRUE)` nodes; replaces `Filter(FALSE)` with EmptyRelation |

### Cost Model

| Operator | Formula |
|----------|---------|
| SeqScan | `pageCost × pages` |
| HashJoin | `1.5 × innerRows + outerRows` |
| NLJoin | `outerRows × innerRows × 0.01` |
| Sort | `rows × log₂(rows) × 0.1` |
| HashAgg | `1.2 × rows` |

## Setup

### Prerequisites

- Go 1.22+
- Node.js 20+

### Install

```bash
# Backend
go mod download

# Frontend
cd web && npm install
```

### Run (development)

```bash
make dev
# Starts Go server on :8080 and Vite dev server on :5173
```

Open http://localhost:5173

### Run (production)

```bash
make build          # builds Go binary to bin/server
cd web && npm run build   # builds frontend to web/dist/
./bin/server        # serves API + static files on :8080
```

### Other targets

```bash
make test    # go test ./... -v -count=1
make lint    # go vet + tsc --noEmit
make seed    # POST /api/schema/seed (requires running server)
```

## API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/query` | Execute SQL, return rows + optional plan/stats |
| POST | `/api/explain` | Parse + plan only (no execution) |
| GET | `/api/schema` | List tables and columns |
| GET | `/api/stats` | Table statistics and histograms |
| POST | `/api/schema/table` | CREATE TABLE via SQL |
| POST | `/api/schema/seed` | Reset and re-seed demo data |

### POST /api/query

```json
{
  "sql": "SELECT ...",
  "options": { "explain": true, "includeStats": true }
}
```

Response includes `columns`, `rows`, `rowCount`, `executionTimeMs`, and optionally `plan` (logical/optimized/physical trees), `optimizationSteps`, and `stats`.

## Frontend

Three pages:

- **Playground** — Monaco SQL editor, result table, plan tree viewer (logical / optimized / physical tabs), optimization steps diff
- **Schema** — Browse tables, columns, row counts; re-seed button
- **Statistics** — Per-column stats, histograms rendered as bar charts

### Features

- Ctrl+Enter to execute
- SQL auto-completion (table and column names)
- Error squiggles: parse/analysis errors highlighted in editor with line/col
- 5 preset sample queries
- CSV export of results
- Resizable panels (editor / results / plan viewer)
- React Flow plan tree with dagre auto-layout; color-coded by operator type
- Recharts histograms on the Stats page

## Sample Queries

```sql
-- Filter
SELECT * FROM customers WHERE country = 'US' LIMIT 10

-- Join + aggregate
SELECT c.name, COUNT(o.id) AS orders
FROM customers c
JOIN orders o ON c.id = o.customer_id
GROUP BY c.name ORDER BY orders DESC LIMIT 10

-- Three-way join
SELECT c.name, p.name, SUM(o.amount) AS total
FROM orders o
JOIN customers c ON o.customer_id = c.id
JOIN products p ON o.product_id = p.id
GROUP BY c.name, p.name ORDER BY total DESC LIMIT 5

-- HAVING
SELECT customer_id, SUM(amount) total
FROM orders GROUP BY customer_id
HAVING COUNT(*) > 5 ORDER BY total DESC LIMIT 10

-- Group by status
SELECT status, COUNT(*) AS cnt FROM orders GROUP BY status ORDER BY cnt DESC
```

## Seed Data

| Table | Rows | Description |
|-------|------|-------------|
| customers | 100 | name, email, country (5 countries) |
| products | 50 | name, category (5 categories), price 1–500 |
| orders | 1000 | customer_id, product_id, amount, status (60/20/10/10 distribution) |
