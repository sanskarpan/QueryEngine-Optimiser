# QueryEngine Optimizer

A from-scratch, production-grade SQL query engine implemented in Go with a rule-based optimizer (RBO), cost-based optimizer (CBO), and an interactive web frontend for exploring query execution plans.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP API (chi router)                 │
│  POST /api/query  POST /api/explain  GET /api/schema  ...    │
└───────────────────────────┬─────────────────────────────────┘
                            │ SQL string
                            ▼
                    ┌───────────────┐
                    │     Lexer     │  Tokenises SQL into typed tokens
                    └───────┬───────┘
                            │ []Token
                            ▼
                    ┌───────────────┐
                    │    Parser     │  Recursive-descent; builds AST
                    └───────┬───────┘
                            │ AST node
                            ▼
                    ┌───────────────┐
                    │   Analyzer    │  Name resolution, type checks
                    └───────┬───────┘
                            │ validated AST
                            ▼
                    ┌───────────────┐
                    │ Logical Plan  │  LogicalScan / Filter / Join / …
                    │   Builder     │
                    └───────┬───────┘
                            │ LogicalPlan tree
                            ▼
                    ┌───────────────┐
                    │   Optimizer   │  RBO (fixed-point) + CBO (DP join order)
                    └───────┬───────┘
                            │ optimised LogicalPlan
                            ▼
                    ┌───────────────┐
                    │Physical Plan  │  SeqScan / HashJoin / Sort / Window / …
                    │   Builder     │
                    └───────┬───────┘
                            │ PhysicalPlan tree
                            ▼
                    ┌───────────────┐
                    │   Executor    │  Volcano iterator (Open/Next/Close)
                    └───────┬───────┘
                            │ [][]Value
                            ▼
                       Result rows
```

### Optimization Rules

| Rule | Effect |
|------|--------|
| PredicatePushdown | Moves filters closer to scans, through joins and projections |
| ProjectionPushdown | Drops unreferenced columns below joins |
| ConstantFolding | Evaluates `3 > 2 → TRUE`, `x AND TRUE → x`, etc. |
| EliminateDeadFilter | Removes `Filter(TRUE)`; replaces `Filter(FALSE)` with EmptyRelation |

### Cost Model

| Operator | Formula |
|----------|---------|
| SeqScan | `pageCost × pages` |
| HashJoin | `1.5 × innerRows + outerRows` |
| NLJoin | `outerRows × innerRows × 0.01` |
| Sort | `rows × log₂(rows) × 0.1` |
| HashAgg | `1.2 × rows` |

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+ (for the web frontend)

### Build and run (native)

```bash
# Download dependencies
go mod download

# Build the server binary
make build

# Run
./bin/server
# Server starts on :8080
```

### Run with Docker

```bash
# Build the image
make docker-build

# Run with compose
make docker-run
# Server starts on :8080
```

### Development mode (live reload)

```bash
make dev
# Go server on :8080, Vite dev server on :5173
```

Open `http://localhost:5173`

### First query

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT name, country FROM customers LIMIT 5"}' | jq .
```

## Configuration

| Environment variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | TCP port the HTTP server listens on |
| `CORS_ORIGIN` | `*` | Value for `Access-Control-Allow-Origin` header |
| `SEED_TOKEN` | _(empty)_ | If set, `POST /api/schema/seed` requires `Authorization: Bearer <token>` |

## API Reference

### POST /api/query

Execute a SQL statement and return the result set.

**Request**
```json
{
  "sql": "SELECT c.name, COUNT(o.id) AS orders FROM customers c JOIN orders o ON c.id = o.customer_id GROUP BY c.name ORDER BY orders DESC LIMIT 5",
  "options": {
    "explain": true,
    "includeStats": true
  }
}
```

**Response**
```json
{
  "columns": ["name", "orders"],
  "rows": [["Alice", 12], ["Bob", 9]],
  "rowCount": 2,
  "executionTimeMs": 3,
  "plan": {
    "logical":   { ... },
    "optimized": { ... },
    "physical":  { ... }
  },
  "optimizationSteps": [
    { "rule": "PredicatePushdown", "applied": true, "description": "Pushed filter below join" }
  ],
  "stats": {
    "rowsScanned": 1100,
    "hashJoins": 1,
    "sortOperations": 1,
    "rowsProduced": 2
  }
}
```

**Error response** (HTTP 400 / 422 / 500)
```json
{
  "error": "column \"unknowncol\" not found",
  "stage": "analyzer",
  "line": 0,
  "col": 0
}
```

### POST /api/explain

Parse and plan a SQL statement without executing it.

**Request**
```json
{ "sql": "SELECT * FROM customers WHERE country = 'US'" }
```

**Response**
```json
{
  "plan": {
    "logical":   { ... },
    "optimized": { ... },
    "physical":  { ... }
  }
}
```

### GET /api/schema

List all tables with their column definitions and row counts.

**Response**
```json
{
  "tables": [
    {
      "name": "customers",
      "columns": [
        { "name": "id",      "type": "INT",  "nullable": false, "primaryKey": true },
        { "name": "name",    "type": "TEXT", "nullable": true,  "primaryKey": false },
        { "name": "country", "type": "TEXT", "nullable": true,  "primaryKey": false }
      ],
      "rowCount": 100
    }
  ]
}
```

### GET /api/stats

Return per-column statistics (distinct count, null count, min/max, histogram).

**Response**
```json
{
  "tables": {
    "customers": {
      "rowCount": 100,
      "pageCount": 2,
      "columns": {
        "country": {
          "distinctCount": 5,
          "nullCount": 0,
          "minValue": "AU",
          "maxValue": "US",
          "histogram": [
            { "low": "AU", "high": "CA", "frequency": 40 }
          ]
        }
      }
    }
  }
}
```

### POST /api/schema/table

Create a table using a `CREATE TABLE` SQL statement.

**Request**
```json
{ "sql": "CREATE TABLE events (id INT PRIMARY KEY, name TEXT NOT NULL, ts INT)" }
```

**Response** (HTTP 201)
```json
{ "table": "events" }
```

### POST /api/schema/seed

Drop all tables and re-seed the demo dataset (customers, products, orders).

**Request** — no body required. If `SEED_TOKEN` is set, include:
```
Authorization: Bearer <token>
```

**Response**
```json
{ "status": "seeded" }
```

### GET /health

Liveness check.

**Response**
```json
{ "status": "ok", "version": "(devel)" }
```

## Supported SQL Syntax

| Feature | Example |
|---------|---------|
| SELECT columns | `SELECT id, name FROM t` |
| Wildcard | `SELECT * FROM t` |
| Aliasing | `SELECT name AS n FROM t AS tbl` |
| Arithmetic | `SELECT price * 1.1 AS adjusted FROM products` |
| String concat | `SELECT first_name \|\| ' ' \|\| last_name FROM t` |
| WHERE | `WHERE age > 18 AND country = 'US'` |
| LIKE / NOT LIKE | `WHERE name LIKE 'A%'` |
| IS NULL / IS NOT NULL | `WHERE email IS NOT NULL` |
| BETWEEN | `WHERE amount BETWEEN 100 AND 500` |
| IN / NOT IN | `WHERE status IN ('pending', 'shipped')` |
| INNER JOIN | `FROM a JOIN b ON a.id = b.a_id` |
| LEFT JOIN | `FROM a LEFT JOIN b ON a.id = b.a_id` |
| RIGHT JOIN | `FROM a RIGHT JOIN b ON a.id = b.a_id` |
| FULL OUTER JOIN | `FROM a FULL OUTER JOIN b ON a.id = b.a_id` |
| CROSS JOIN | `FROM a CROSS JOIN b` |
| Subquery in FROM | `FROM (SELECT ...) AS sub` |
| Scalar subquery | `SELECT (SELECT COUNT(*) FROM orders) AS total` |
| EXISTS subquery | `WHERE EXISTS (SELECT 1 FROM orders WHERE ...)` |
| IN subquery | `WHERE id IN (SELECT customer_id FROM orders)` |
| CTEs | `WITH top AS (SELECT ...) SELECT * FROM top` |
| GROUP BY | `GROUP BY country` |
| HAVING | `HAVING COUNT(*) > 5` |
| ORDER BY | `ORDER BY amount DESC NULLS LAST` |
| LIMIT / OFFSET | `LIMIT 10 OFFSET 20` |
| DISTINCT | `SELECT DISTINCT country FROM customers` |
| UNION / UNION ALL | `SELECT ... UNION ALL SELECT ...` |
| INTERSECT / EXCEPT | `SELECT ... INTERSECT SELECT ...` |
| Window functions | `ROW_NUMBER() / RANK() / DENSE_RANK() / NTILE(n) OVER (PARTITION BY ... ORDER BY ...)` |
| LAG / LEAD | `LAG(col, 1, 0) OVER (ORDER BY ts)` |
| FIRST_VALUE / LAST_VALUE | `FIRST_VALUE(amount) OVER (PARTITION BY customer_id ORDER BY ts)` |
| NTH_VALUE | `NTH_VALUE(amount, 2) OVER (ORDER BY ts)` |
| Aggregate windows | `SUM(amount) OVER (ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)` |
| RANGE mode | `SUM(val) OVER (ORDER BY category RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)` |
| CASE WHEN | `CASE WHEN x > 0 THEN 'positive' ELSE 'other' END` |
| CAST | `CAST(price AS FLOAT)` |
| INSERT INTO | `INSERT INTO t (col1, col2) VALUES (1, 'a'), (2, 'b')` |
| INSERT … SELECT | `INSERT INTO archive SELECT * FROM orders WHERE status = 'shipped'` |
| UPDATE | `UPDATE products SET price = price * 1.1 WHERE category = 'Electronics'` |
| DELETE | `DELETE FROM orders WHERE status = 'cancelled'` |
| CREATE TABLE | `CREATE TABLE t (id INT PRIMARY KEY, name TEXT NOT NULL)` |
| CREATE TABLE AS | `CREATE TABLE summary AS SELECT country, COUNT(*) AS n FROM customers GROUP BY country` |
| DROP TABLE | `DROP TABLE IF EXISTS t` |
| ALTER TABLE | `ALTER TABLE t ADD COLUMN score FLOAT` |
| ALTER TABLE | `ALTER TABLE t DROP COLUMN score` |
| ALTER TABLE | `ALTER TABLE t RENAME COLUMN old TO new` |
| ALTER TABLE | `ALTER TABLE t RENAME TO new_name` |
| EXPLAIN | `EXPLAIN SELECT * FROM customers` |

### Built-in Functions

| Category | Functions |
|----------|-----------|
| String | `UPPER`, `LOWER`, `LENGTH`, `TRIM`, `LTRIM`, `RTRIM`, `SUBSTR`, `REPLACE`, `CONCAT`, `CONCAT_WS`, `LPAD`, `RPAD`, `REVERSE`, `REPEAT` |
| Numeric | `ABS`, `ROUND`, `FLOOR`, `CEIL`, `POWER`, `SQRT`, `MOD`, `LOG`, `LOG2`, `LOG10`, `SIGN`, `TRUNCATE` |
| Date/Time | `NOW`, `DATE`, `YEAR`, `MONTH`, `DAY`, `HOUR`, `MINUTE`, `SECOND`, `EXTRACT`, `DATE_TRUNC` |
| Type | `COALESCE`, `NULLIF`, `IFNULL`, `IIF`, `CAST` |
| Aggregate | `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `STDDEV`, `VARIANCE` |

## Project Structure

```
QueryEngine-Optimiser/
├── api/                        # HTTP API layer
│   ├── server.go               # Server struct, route registration
│   ├── handler.go              # Request handlers
│   ├── middleware.go           # CORS, body-size limit
│   └── dto.go                  # Request/response types
├── cmd/server/
│   └── main.go                 # Entry point: server setup and graceful shutdown
├── internal/
│   ├── lexer/                  # SQL tokenizer
│   │   ├── token.go            # TokenType constants and Token struct
│   │   └── lexer.go            # Lexer implementation
│   ├── ast/                    # Abstract syntax tree
│   │   ├── nodes.go            # All AST node types
│   │   ├── printer.go          # Expression/statement pretty-printing
│   │   └── visitor.go          # Visitor interface for AST traversal
│   ├── parser/                 # SQL parser
│   │   ├── errors.go           # ParseError type
│   │   └── parser.go           # Recursive-descent parser
│   ├── analyzer/
│   │   └── analyzer.go         # Semantic analysis, scope resolution
│   ├── catalog/                # Schema metadata
│   │   ├── types.go            # DataType, Value, arithmetic/comparison
│   │   ├── catalog.go          # Catalog (thread-safe table registry)
│   │   └── table.go            # Table and Column types
│   ├── storage/                # In-memory storage
│   │   ├── storage.go          # Storage (table registry, RWMutex)
│   │   ├── heap.go             # HeapTable (in-memory row store)
│   │   ├── tuple.go            # Tuple type
│   │   └── seed.go             # Demo data seeding
│   ├── planner/
│   │   ├── logical/            # Logical planning
│   │   │   ├── nodes.go        # LogicalScan, Filter, Join, Agg, Window, …
│   │   │   ├── plan.go         # Plan interface and JSON serialization
│   │   │   ├── builder.go      # AST → LogicalPlan
│   │   │   └── printer.go      # Plan tree printer
│   │   └── physical/           # Physical planning
│   │       ├── nodes.go        # SeqScan, HashJoin, Sort, Window, …
│   │       ├── plan.go         # Plan interface
│   │       └── builder.go      # LogicalPlan → PhysicalPlan (cost-based)
│   ├── optimizer/
│   │   ├── optimizer.go        # Fixed-point rule engine
│   │   └── rule/               # Optimization rules
│   │       ├── rule.go         # Rule interface
│   │       ├── constant_folding.go
│   │       ├── predicate_pushdown.go
│   │       └── projection_pushdown.go
│   ├── executor/
│   │   ├── executor.go         # Execute() entry point, buildOperator()
│   │   └── operators/          # Volcano iterator operators
│   │       ├── operator.go     # Operator interface
│   │       ├── expression.go   # EvalExpr, built-in functions
│   │       ├── seq_scan.go
│   │       ├── filter.go
│   │       ├── projection.go
│   │       ├── hash_join.go
│   │       ├── nl_join.go
│   │       ├── sort_merge_join.go
│   │       ├── hash_agg.go
│   │       ├── sort.go
│   │       ├── limit.go
│   │       ├── window.go
│   │       ├── set_op.go
│   │       ├── insert.go
│   │       ├── update.go
│   │       ├── delete.go
│   │       ├── ddl.go
│   │       └── empty.go
│   ├── exectypes/
│   │   └── types.go            # ExecContext, Tuple (shared to avoid circular imports)
│   └── stats/
│       ├── stats.go            # TableStats, ColumnStats, Bucket types
│       └── collector.go        # Statistics collection from heap tables
├── web/                        # React + Monaco + React Flow frontend
├── Makefile
├── Dockerfile
├── docker-compose.yml
├── CONTRIBUTING.md
└── CHANGELOG.md
```

## Development

```bash
make test        # go test ./... -v -count=1
make lint        # go vet + tsc --noEmit
make build       # build Go binary → bin/server
make docker-build  # docker build
make seed        # POST /api/schema/seed (server must be running)
```

## Seed Data

| Table | Rows | Columns |
|-------|------|---------|
| customers | 100 | id, name, email, country (5 countries) |
| products | 50 | id, name, category (5 categories), price (1–500) |
| orders | 1000 | id, customer_id, product_id, amount, status |
