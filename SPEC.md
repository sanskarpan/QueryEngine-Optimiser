# Query Engine & Optimizer — Technical Specification

## Overview

A full-stack query engine built from scratch. The backend is written in Go and implements a complete SQL processing pipeline: lexing → parsing → semantic analysis → logical planning → rule-based and cost-based optimization → physical planning → execution. The frontend is React + TypeScript and provides a live SQL playground, query plan visualizer, and statistics explorer.

---

## Tech Stack

### Backend
| Layer | Technology |
|---|---|
| Language | Go 1.22+ |
| HTTP Framework | `net/http` (stdlib) + `chi` router |
| API Style | REST + JSON |
| Testing | `testing` stdlib + `testify` |
| No ORM, no external DB | Everything in-memory |

### Frontend
| Layer | Technology |
|---|---|
| Language | TypeScript 5+ |
| Framework | React 18 + Vite 5 |
| Styling | Tailwind CSS v3 |
| SQL Editor | Monaco Editor (`@monaco-editor/react`) |
| Plan Visualizer | React Flow (`@xyflow/react`) |
| Charts | Recharts |
| UI Components | shadcn/ui |
| State Management | Zustand |
| HTTP Client | `ky` (lightweight fetch wrapper) |

---

## Project Structure

```
query-engine/
├── cmd/
│   └── server/
│       └── main.go               # Entry point, wires everything together
├── internal/
│   ├── lexer/
│   │   ├── lexer.go              # Tokenizer
│   │   ├── token.go              # Token types and definitions
│   │   └── lexer_test.go
│   ├── parser/
│   │   ├── parser.go             # Recursive-descent SQL parser
│   │   ├── parser_test.go
│   │   └── errors.go             # Parse error types
│   ├── ast/
│   │   ├── nodes.go              # All AST node types
│   │   ├── visitor.go            # Visitor interface for tree walking
│   │   └── printer.go            # AST pretty-printer
│   ├── catalog/
│   │   ├── catalog.go            # Schema registry
│   │   ├── table.go              # Table and column definitions
│   │   └── types.go              # SQL data types
│   ├── analyzer/
│   │   ├── analyzer.go           # Semantic analysis: name resolution, type checking
│   │   └── analyzer_test.go
│   ├── planner/
│   │   ├── logical/
│   │   │   ├── plan.go           # Logical plan node interfaces
│   │   │   ├── nodes.go          # Scan, Filter, Project, Join, Agg, Sort, Limit
│   │   │   ├── builder.go        # AST → Logical plan builder
│   │   │   └── printer.go        # Logical plan pretty-printer
│   │   └── physical/
│   │       ├── plan.go           # Physical plan node interfaces
│   │       ├── nodes.go          # SeqScan, HashJoin, NLJoin, SortMergeJoin, HashAgg, Sort
│   │       └── builder.go        # Logical → Physical plan builder
│   ├── optimizer/
│   │   ├── optimizer.go          # Orchestrates RBO + CBO pipeline
│   │   ├── rule/
│   │   │   ├── rule.go           # Rule interface
│   │   │   ├── predicate_pushdown.go
│   │   │   ├── projection_pushdown.go
│   │   │   ├── constant_folding.go
│   │   │   ├── eliminate_subquery.go
│   │   │   └── registry.go       # Rule registry and application order
│   │   └── cost/
│   │       ├── estimator.go      # Cardinality and cost estimation
│   │       ├── model.go          # Cost model (CPU + I/O)
│   │       ├── join_order.go     # DP-based join reordering
│   │       └── histogram.go      # Column statistics / histograms
│   ├── executor/
│   │   ├── executor.go           # Volcano iterator model orchestrator
│   │   ├── context.go            # Execution context (memory budget, etc.)
│   │   └── operators/
│   │       ├── operator.go       # Operator interface: Open/Next/Close
│   │       ├── seq_scan.go
│   │       ├── filter.go
│   │       ├── projection.go
│   │       ├── nl_join.go        # Nested Loop Join
│   │       ├── hash_join.go      # Hash Join
│   │       ├── sort_merge_join.go
│   │       ├── hash_agg.go       # Hash-based aggregation
│   │       ├── sort.go
│   │       ├── limit.go
│   │       └── expression.go     # Expression evaluator
│   ├── storage/
│   │   ├── storage.go            # Storage engine interface
│   │   ├── heap.go               # In-memory heap storage (row store)
│   │   └── tuple.go              # Tuple / row representation
│   └── stats/
│       ├── stats.go              # Table and column statistics
│       └── collector.go          # Stats collection from data
├── api/
│   ├── server.go                 # HTTP server setup
│   ├── handler.go                # All HTTP handlers
│   ├── middleware.go             # CORS, logging, recovery
│   └── dto.go                    # Request/Response DTOs
├── web/                          # Frontend (Vite React app)
│   ├── src/
│   │   ├── main.tsx
│   │   ├── App.tsx
│   │   ├── pages/
│   │   │   ├── Playground.tsx    # Main SQL editor + results page
│   │   │   ├── Schema.tsx        # Catalog / schema browser
│   │   │   └── Statistics.tsx    # Table statistics dashboard
│   │   ├── components/
│   │   │   ├── Editor/
│   │   │   │   ├── SQLEditor.tsx          # Monaco editor wrapper
│   │   │   │   └── Toolbar.tsx
│   │   │   ├── PlanViewer/
│   │   │   │   ├── PlanTree.tsx           # React Flow tree
│   │   │   │   ├── PlanNode.tsx           # Custom node renderer
│   │   │   │   └── OptimizationDiff.tsx   # Before/after diff
│   │   │   ├── Results/
│   │   │   │   ├── ResultTable.tsx
│   │   │   │   └── ExecutionStats.tsx
│   │   │   └── shared/
│   │   │       ├── Badge.tsx
│   │   │       └── CostBar.tsx
│   │   ├── store/
│   │   │   └── queryStore.ts     # Zustand store
│   │   ├── api/
│   │   │   └── client.ts         # API client
│   │   └── types/
│   │       └── index.ts          # TypeScript types mirroring backend DTOs
│   ├── package.json
│   ├── vite.config.ts
│   ├── tailwind.config.ts
│   └── tsconfig.json
├── testdata/
│   ├── seed.sql                  # Sample schema + data
│   └── queries/                  # Benchmark SQL queries
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Backend: Component Specifications

### 1. Lexer (`internal/lexer`)

Converts raw SQL string → stream of tokens.

**Token Types:**
```
Keywords:   SELECT, FROM, WHERE, JOIN, ON, GROUP, BY, HAVING, ORDER,
            LIMIT, OFFSET, INSERT, CREATE, TABLE, AS, AND, OR, NOT,
            IN, BETWEEN, LIKE, IS, NULL, TRUE, FALSE, INNER, LEFT,
            RIGHT, CROSS, DISTINCT, ALL, ASC, DESC, CASE, WHEN,
            THEN, ELSE, END, EXISTS, WITH, UNION, INTERSECT, EXCEPT

Operators:  = != < > <= >= + - * / % || 

Literals:   INTEGER, FLOAT, STRING, BOOLEAN

Symbols:    ( ) , . ; *

Meta:       EOF, ILLEGAL, IDENT
```

**Requirements:**
- Case-insensitive keyword recognition
- Support single and double quoted string literals
- Support `--` and `/* */` comments (skip silently)
- Track line/column for error reporting
- Expose `Peek()` and `Next()` interface

### 2. Parser (`internal/parser`)

Recursive-descent parser. Converts token stream → AST.

**Supported SQL Grammar:**

```
Statement    ::= SelectStmt | CreateTableStmt | InsertStmt
SelectStmt   ::= SELECT [DISTINCT] SelectList
                 FROM TableRef
                 [JoinClause*]
                 [WHERE Expr]
                 [GROUP BY ExprList [HAVING Expr]]
                 [ORDER BY SortSpec*]
                 [LIMIT Expr [OFFSET Expr]]

TableRef     ::= TableName [AS alias] | SubQuery AS alias
JoinClause   ::= [INNER|LEFT|RIGHT|CROSS] JOIN TableRef ON Expr
SelectList   ::= * | SelectExpr (,SelectExpr)*
SelectExpr   ::= Expr [AS alias]

Expr         ::= Literal | ColumnRef | FuncCall | BinaryExpr
               | UnaryExpr | CaseExpr | InExpr | BetweenExpr
               | SubQueryExpr | IsNullExpr

BinaryExpr   ::= Expr (=|!=|<|>|<=|>=|+|-|*|/|AND|OR|LIKE) Expr
FuncCall     ::= IDENT ( ExprList? )   -- COUNT, SUM, AVG, MIN, MAX, etc.
ColumnRef    ::= [TableAlias.] ColumnName

CreateTableStmt ::= CREATE TABLE name (ColDef (, ColDef)*)
ColDef          ::= name Type [NOT NULL] [PRIMARY KEY]
Type            ::= INT | INTEGER | FLOAT | TEXT | VARCHAR(n) | BOOL | BOOLEAN

InsertStmt   ::= INSERT INTO name (ColList) VALUES (ValList)
```

**Requirements:**
- Operator precedence handled correctly (Pratt parsing for expressions)
- Clear, structured error messages with line/column info
- Produce fully-typed AST nodes (see `ast/nodes.go`)

### 3. AST Nodes (`internal/ast`)

Every node carries position info (`Pos{Line, Col}`).

Key node types:
- `SelectStatement`, `CreateTableStatement`, `InsertStatement`
- `JoinClause` with `JoinType` enum
- Expressions: `BinaryExpr`, `UnaryExpr`, `ColumnRef`, `Literal`, `FuncCall`, `CaseExpr`, `InExpr`, `BetweenExpr`, `SubqueryExpr`, `IsNullExpr`
- `SortSpec` with direction

### 4. Catalog (`internal/catalog`)

In-memory schema registry.

```go
type Catalog struct {
    tables map[string]*Table
}

type Table struct {
    Name    string
    Columns []Column
    Stats   *TableStats
}

type Column struct {
    Name     string
    Type     DataType
    Nullable bool
    PK       bool
    Index    int   // ordinal position
}

type DataType int
// INT, FLOAT, TEXT, BOOL, NULL
```

**Requirements:**
- Thread-safe reads (tables are registered at startup, read-only after)
- `Lookup(tableName)` returns `(*Table, bool)`
- `Register(table)` called during CREATE TABLE execution
- Pre-populate with seed data on startup (orders, customers, products)

### 5. Semantic Analyzer (`internal/analyzer`)

Walks the AST, validates semantic correctness, and annotates nodes with resolved types.

**Checks performed:**
- Table references exist in catalog
- Column references are unambiguous (resolve `*` to explicit columns)
- Aggregate functions only appear in SELECT/HAVING (not WHERE)
- GROUP BY columns are referenced in SELECT list
- JOIN ON expression references columns from both sides
- Type compatibility in binary expressions
- Subquery cardinality (EXISTS, scalar subqueries)

**Output:** Annotated AST + resolved column metadata per alias scope.

### 6. Logical Planner (`internal/planner/logical`)

Converts annotated AST → relational algebra tree (logical plan).

**Logical Operators:**
```
LogicalScan(table, alias)
LogicalFilter(child, predicate)
LogicalProject(child, expressions, aliases)
LogicalJoin(left, right, joinType, condition)
LogicalAggregate(child, groupBy, aggregates)
LogicalSort(child, sortSpecs)
LogicalLimit(child, count, offset)
LogicalSubquery(select)
```

**Schema propagation:** Each logical node exposes `Schema() []Column` so upstream operators know what columns are available.

### 7. Rule-Based Optimizer (`internal/optimizer/rule`)

Applies transformation rules to the logical plan tree. Rules fire repeatedly until no rule produces a change (fixed-point iteration, max 10 rounds).

**Rules (in application order):**

| Rule | Description |
|---|---|
| `PredicatePushdown` | Push Filter nodes as deep as possible in the tree, past Joins and Aggregations |
| `ProjectionPushdown` | Eliminate columns not referenced downstream; insert Projects below joins |
| `ConstantFolding` | Evaluate constant expressions at plan time: `1+2 → 3`, `true AND false → false` |
| `EliminateDeadFilter` | Remove `WHERE true`, prune `WHERE false` (returns empty) |
| `FlattenNestedLoops` | Convert correlated subqueries to joins where possible |
| `JoinCommutativity` | Flip join sides when beneficial for cost estimation |

**Rule Interface:**
```go
type Rule interface {
    Name() string
    Apply(plan logical.Plan) (logical.Plan, bool) // returns (newPlan, changed)
}
```

### 8. Cost-Based Optimizer (`internal/optimizer/cost`)

Uses statistics to estimate cardinalities and costs, then uses dynamic programming to find the optimal join order.

**Statistics model:**
```go
type TableStats struct {
    RowCount      int64
    PageCount     int64
    Columns       map[string]*ColumnStats
}

type ColumnStats struct {
    DistinctCount int64
    NullCount     int64
    MinValue      any
    MaxValue      any
    Histogram     []Bucket  // equi-depth histogram, 10 buckets
}

type Bucket struct {
    Low, High any
    Frequency int64
}
```

**Cardinality estimation:**
- Base table: from `TableStats.RowCount`
- Filter with equality pred `col = val`: `rowCount / distinctCount`
- Filter with range pred `col > val`: histogram-based
- Inner join: `left.rows * right.rows / max(left.ndv(col), right.ndv(col))`
- Outer join: `max(inner, left.rows)`
- Group by: `min(groupby.ndv_product, child.rows)`

**Cost model (Volcano-style):**
```
SeqScan:      pageCost * pageCount  
HashJoin:     buildCost(inner) + probeCost(outer)
              buildCost = 1.5 * inner.rows
              probeCost = 1.0 * outer.rows
NLJoin:       outer.rows * inner.rows * 0.01
SortMergeJoin: sort(outer) + sort(inner) + merge_cost
HashAgg:      1.2 * child.rows
Sort:         child.rows * log2(child.rows) * 0.1
```

**Join Reordering (DP):**
- Enumerate all subsets of join relations (2^n, n ≤ 10)
- For each subset, try all ways to split into left/right pairs
- Choose minimum-cost plan using memoization
- Emit as a left-deep tree by default (better for pipelining)

### 9. Physical Planner (`internal/planner/physical`)

Converts optimized logical plan → physical execution plan by choosing concrete algorithms.

**Logical → Physical mapping:**

| Logical | Physical options | Selection criterion |
|---|---|---|
| `LogicalScan` | `SeqScan` | Only option (no indexes yet) |
| `LogicalFilter` | `Filter` | Always inline |
| `LogicalProject` | `Projection` | Always inline |
| `LogicalJoin(INNER)` | `HashJoin`, `NLJoin`, `SortMergeJoin` | CBO cost comparison |
| `LogicalJoin(LEFT/RIGHT)` | `HashJoin(outer)`, `NLJoin(outer)` | CBO cost comparison |
| `LogicalAggregate` | `HashAgg`, `SortAgg` | HashAgg default; SortAgg if input already sorted |
| `LogicalSort` | `ExternalSort` | In-memory sort (no spill for now) |
| `LogicalLimit` | `Limit` | Always inline |

### 10. Executor (`internal/executor`)

Implements the Volcano (iterator) model. Every operator implements:

```go
type Operator interface {
    Open(ctx *ExecContext) error
    Next() (*Tuple, error)   // returns nil tuple at EOF
    Close() error
    Schema() []Column
}
```

**Operators:**

`SeqScan` — iterates rows from the heap storage for a given table.

`Filter` — calls child.Next(), evaluates predicate, skips non-matching tuples.

`Projection` — calls child.Next(), evaluates projection expressions, emits new tuples.

`NestedLoopJoin` — outer loop calls left.Next(), inner loop restarts right on each outer tuple, emits matching pairs.

`HashJoin`:
1. Build phase: consume entire right (inner) side, hash on join key into `map[hashKey][]Tuple`
2. Probe phase: stream left (outer) side, probe hash map, emit matches

`SortMergeJoin`:
1. Sort both inputs on join key
2. Merge with two-pointer technique

`HashAggregate`:
- First pass: consume child, hash on group-by keys, accumulate aggregate states
- Second pass: emit one tuple per group with computed aggregate values
- Aggregate states: COUNT (int64), SUM (float64), MIN/MAX (any), AVG (count+sum pair)

`Sort` — consume all tuples from child, sort in-memory using Go's sort.Slice with comparison function built from ORDER BY spec.

`Limit` — count tuples, stop after N, skip first M (OFFSET).

`Expression evaluator` — recursive evaluation of expression trees over a tuple + schema context. Supports arithmetic, comparisons, boolean logic, CASE/WHEN, IS NULL, LIKE (regex-based), IN (linear scan of list).

### 11. Storage (`internal/storage`)

Simple in-memory heap table.

```go
type HeapTable struct {
    mu   sync.RWMutex
    rows []Tuple
}

type Tuple struct {
    Values []Value
}

type Value struct {
    Type    DataType
    IsNull  bool
    IntVal  int64
    FloatVal float64
    StrVal  string
    BoolVal bool
}
```

**Operations:** `Insert(tuple)`, `Scan() []Tuple`, `RowCount() int64`

---

## API Specification

Base URL: `http://localhost:8080/api`

### `POST /query`

Execute a SQL query end-to-end.

**Request:**
```json
{
  "sql": "SELECT o.id, c.name, SUM(o.amount) FROM orders o JOIN customers c ON o.customer_id = c.id GROUP BY o.id, c.name ORDER BY 3 DESC LIMIT 10",
  "options": {
    "explain": true,
    "includeStats": true
  }
}
```

**Response:**
```json
{
  "columns": ["id", "name", "SUM(o.amount)"],
  "rows": [[1, "Alice", 450.00], [2, "Bob", 320.50]],
  "rowCount": 2,
  "executionTimeMs": 12,
  "plan": {
    "logical": { /* logical plan tree */ },
    "optimized": { /* after RBO */ },
    "physical": { /* physical plan tree */ }
  },
  "optimizationSteps": [
    {
      "rule": "PredicatePushdown",
      "applied": true,
      "description": "Pushed filter on orders.status below Join"
    }
  ],
  "stats": {
    "rowsScanned": 1250,
    "hashJoins": 1,
    "sortOperations": 1
  }
}
```

### `GET /schema`

Returns all tables in the catalog.

**Response:**
```json
{
  "tables": [
    {
      "name": "orders",
      "columns": [
        { "name": "id", "type": "INT", "nullable": false, "primaryKey": true },
        { "name": "customer_id", "type": "INT", "nullable": false },
        { "name": "amount", "type": "FLOAT", "nullable": false },
        { "name": "status", "type": "TEXT", "nullable": true }
      ],
      "rowCount": 1000
    }
  ]
}
```

### `GET /stats`

Returns table statistics.

**Response:**
```json
{
  "tables": {
    "orders": {
      "rowCount": 1000,
      "pageCount": 10,
      "columns": {
        "status": {
          "distinctCount": 4,
          "nullCount": 12,
          "histogram": [{ "low": "cancelled", "high": "shipped", "frequency": 250 }]
        }
      }
    }
  }
}
```

### `POST /explain`

Parse and plan only (no execution).

**Request:**
```json
{ "sql": "SELECT ...", "stage": "logical|optimized|physical" }
```

**Response:** Same `plan` section as `/query`, no `rows`.

### `POST /schema/table`

Register a new table (CREATE TABLE).

**Request:**
```json
{
  "sql": "CREATE TABLE products (id INT PRIMARY KEY, name TEXT NOT NULL, price FLOAT)"
}
```

### `POST /schema/seed`

Reset database to seed state.

---

## Plan Tree JSON Schema

Used in API responses and by the frontend visualizer.

```json
{
  "id": "node-1",
  "type": "HashJoin",
  "estimatedRows": 450,
  "actualRows": 423,
  "estimatedCost": 892.4,
  "attributes": {
    "joinType": "INNER",
    "condition": "orders.customer_id = customers.id",
    "algorithm": "HashJoin",
    "buildSide": "right"
  },
  "children": [
    {
      "id": "node-2",
      "type": "SeqScan",
      "estimatedRows": 1000,
      "attributes": { "table": "orders", "alias": "o" },
      "children": []
    },
    {
      "id": "node-3",
      "type": "SeqScan",
      "estimatedRows": 100,
      "attributes": { "table": "customers", "alias": "c" },
      "children": []
    }
  ]
}
```

---

## Frontend: Component Specifications

### Page: Playground (`/`)

**Layout:** Three-panel layout (resizable with `react-resizable-panels`):
- Left panel: SQL editor + run button + sample queries dropdown
- Top-right panel: Results table with pagination
- Bottom-right panel: Query plan viewer (tabbed: Logical / Optimized / Physical)

**SQL Editor:**
- Monaco Editor with custom SQL syntax highlighting
- Ctrl+Enter to execute
- Error squiggles using Monaco markers (backend parse errors mapped back to line/col)
- Auto-complete table/column names from schema (Monaco completion provider)

**Plan Viewer:**
- React Flow tree layout (top-down, `dagre` layout algorithm via `@dagrejs/dagre`)
- Custom node colors by operator type:
  - Scan → blue
  - Join → amber
  - Aggregation → purple
  - Sort/Limit → teal
  - Filter/Project → gray
- Each node shows: operator name, estimated rows, cost
- Click node → opens sidebar detail panel
- "Diff" toggle: highlights nodes that changed between Logical and Optimized plans

**Optimization Steps panel:** Timeline showing each rule that fired, whether it changed anything, and a plain-English description.

### Page: Schema (`/schema`)

- Table list sidebar
- Column details table with types, nullability, PK indicators
- Row count badge
- Inline `INSERT INTO` mini-editor per table

### Page: Statistics (`/stats`)

- Per-table card
- Column stats: distinct count, null %, min/max
- Histogram bar chart (Recharts `BarChart`) per column

---

## Seed Data

The engine starts with three pre-populated tables:

**customers** (100 rows): id, name, email, country, created_at

**products** (50 rows): id, name, category, price, stock_quantity

**orders** (1000 rows): id, customer_id, product_id, quantity, amount, status, created_at
  - status ∈ {pending, processing, shipped, cancelled}
  - Realistic distribution: 60% shipped, 20% processing, 10% pending, 10% cancelled

**Sample benchmark queries** (exposed as presets in the UI):
1. Simple scan with filter: `SELECT * FROM customers WHERE country = 'US'`
2. Two-table join: `SELECT c.name, COUNT(o.id) FROM customers c JOIN orders o ON c.id = o.customer_id GROUP BY c.name`
3. Three-way join: `SELECT c.name, p.name, SUM(o.amount) FROM orders o JOIN customers c ON o.customer_id = c.id JOIN products p ON o.product_id = p.id GROUP BY c.name, p.name ORDER BY 3 DESC LIMIT 5`
4. Subquery: `SELECT * FROM customers WHERE id IN (SELECT customer_id FROM orders WHERE status = 'cancelled')`
5. Aggregation with HAVING: `SELECT customer_id, SUM(amount) total FROM orders GROUP BY customer_id HAVING total > 500 ORDER BY total DESC`

---

## Error Handling

All errors propagate through a typed error system:

```go
type QueryError struct {
    Stage   string  // "lexer", "parser", "analyzer", "planner", "executor"
    Message string
    Line    int
    Col     int
}
```

HTTP responses:
- `400 Bad Request` — parse/analysis errors (include line/col for editor squiggles)
- `422 Unprocessable Entity` — semantic errors
- `500 Internal Server Error` — executor panics (recovered, logged)
- `200 OK` — always includes `error` field if partial failure

---

## Non-Goals (out of scope for v1)

- Disk-based storage (no B-trees, WAL, or buffer pool)
- Transactions / MVCC
- Index scans (no B-tree/hash index structures)
- Window functions
- CTEs (`WITH` clauses)
- DDL beyond `CREATE TABLE`
- Multi-user / concurrency
- EXPLAIN ANALYZE timing per node (only total)
- Query result caching