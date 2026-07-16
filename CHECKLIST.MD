# Implementation Checklist

Track progress here. Check items as you complete them. Work **strictly in order** — each phase depends on the previous.

---

## Phase 0 — Project Scaffolding

### Go Module
- [ ] `go mod init github.com/yourname/query-engine`
- [ ] Add dependencies: `github.com/go-chi/chi/v5`, `github.com/stretchr/testify`
- [ ] Create `Makefile` with targets: `build`, `test`, `run`, `lint`, `seed`
- [ ] Create `cmd/server/main.go` with stub `main()` that prints "starting server"

### Frontend Scaffold
- [ ] `cd web && npm create vite@latest . -- --template react-ts`
- [ ] Install deps: `tailwindcss`, `@monaco-editor/react`, `@xyflow/react`, `@dagrejs/dagre`, `recharts`, `zustand`, `ky`, `react-resizable-panels`
- [ ] Install shadcn/ui: `npx shadcn@latest init`
- [ ] Install shadcn components: `button`, `badge`, `table`, `tabs`, `sheet`, `tooltip`, `card`, `separator`, `scroll-area`
- [ ] Configure Tailwind, add CSS variables
- [ ] Create `vite.config.ts` with proxy: `/api` → `http://localhost:8080`
- [ ] Verify `npm run dev` runs without errors

---

## Phase 1 — Lexer

- [ ] Define all token types in `internal/lexer/token.go`
- [ ] Implement `Lexer` struct with `input`, `pos`, `line`, `col` fields
- [ ] Implement `Next() Token` — main dispatch method
- [ ] Handle whitespace and newlines (increment line counter on `\n`)
- [ ] Lex integer literals
- [ ] Lex float literals (decimal point detection)
- [ ] Lex single-quoted string literals with escape handling (`''` → `'`)
- [ ] Lex double-quoted identifiers
- [ ] Lex identifiers and keywords (case-insensitive lookup table)
- [ ] Lex all operators: `=`, `!=`, `<>`, `<`, `>`, `<=`, `>=`, `+`, `-`, `*`, `/`, `%`, `||`
- [ ] Lex symbols: `(`, `)`, `,`, `.`, `;`
- [ ] Skip single-line comments (`--` to end of line)
- [ ] Skip multi-line comments (`/* ... */`)
- [ ] Return `EOF` token at end of input
- [ ] Return `ILLEGAL` token for unrecognized characters (with position)
- [ ] Implement `Peek() Token` (lookahead without consuming)
- [ ] Write tests: all token types, keywords, edge cases (empty string, just whitespace, unterminated string)
- [ ] Write tests: line/col tracking across multi-line inputs

---

## Phase 2 — AST Node Types

- [ ] Define `Pos{Line, Col}` struct
- [ ] Define `Statement` interface with `statementNode()` marker method
- [ ] Define `Expression` interface with `expressionNode()` marker method
- [ ] Implement `SelectStatement` node (all fields: distinct, columns, from, joins, where, groupBy, having, orderBy, limit, offset)
- [ ] Implement `CreateTableStatement` node
- [ ] Implement `InsertStatement` node
- [ ] Implement expression nodes: `BinaryExpr`, `UnaryExpr`, `ColumnRef`, `IntLiteral`, `FloatLiteral`, `StringLiteral`, `BoolLiteral`, `NullLiteral`
- [ ] Implement `FunctionCall` node (name + args slice)
- [ ] Implement `CaseExpr` (when/then pairs + optional else)
- [ ] Implement `InExpr` (expr + list or subquery)
- [ ] Implement `BetweenExpr` (expr + low + high)
- [ ] Implement `IsNullExpr` (expr + negated bool)
- [ ] Implement `SubqueryExpr` (wraps SelectStatement)
- [ ] Implement `JoinClause` (type enum + table ref + condition)
- [ ] Implement `SortSpec` (expr + direction)
- [ ] Implement `TableRef` (name + alias + optional subquery)
- [ ] Implement `AliasExpr` (expr + alias string)
- [ ] Implement `Visitor` interface with methods for each node type
- [ ] Implement `ASTPrinter` that produces readable indented string representation

---

## Phase 3 — Parser

- [ ] Implement `Parser` struct wrapping `Lexer` with `current` and `peek` token fields
- [ ] Implement `advance()`, `expect(tokenType)`, `check(tokenType)`, `match(...tokenTypes)` helpers
- [ ] Implement `ParseStatement()` top-level dispatcher
- [ ] Implement `parseSelect()`: SELECT keyword, DISTINCT flag, column list
- [ ] Implement `parseSelectColumns()`: handle `*`, expressions with optional `AS alias`
- [ ] Implement `parseFrom()`: table name with optional alias, subquery with alias
- [ ] Implement `parseJoin()`: parse zero or more JOIN clauses with type + ON condition
- [ ] Implement `parseWhere()`: optional WHERE + expression
- [ ] Implement `parseGroupBy()`: GROUP BY + expression list + optional HAVING
- [ ] Implement `parseOrderBy()`: ORDER BY + sort specs with ASC/DESC
- [ ] Implement `parseLimit()`: LIMIT + optional OFFSET
- [ ] Implement expression parser (Pratt / precedence climbing):
  - [ ] Precedence levels: OR (1), AND (2), NOT (3), comparison (4), addition (5), multiplication (6), unary (7)
  - [ ] Parse `IN (list)` and `IN (subquery)` 
  - [ ] Parse `BETWEEN low AND high`
  - [ ] Parse `IS NULL` / `IS NOT NULL`
  - [ ] Parse `LIKE pattern`
  - [ ] Parse `EXISTS (subquery)`
  - [ ] Parse `CASE WHEN ... THEN ... ELSE ... END`
  - [ ] Parse function calls: `name(args)`, `COUNT(*)`
  - [ ] Parse column references: `table.column` and bare `column`
  - [ ] Parse grouped expressions: `(expr)`
  - [ ] Parse subquery expressions: `(SELECT ...)`
- [ ] Implement `parseCreateTable()`: column definitions with types, NOT NULL, PRIMARY KEY
- [ ] Implement `parseInsert()`: table name, column list, VALUES list
- [ ] Error recovery: produce helpful errors with position info; don't panic
- [ ] Write parser tests for all SELECT variants
- [ ] Write parser tests for all expression types
- [ ] Write parser tests: precedence (`1+2*3` = `1+(2*3)`)
- [ ] Write parser tests: error cases (missing FROM, unmatched parens)
- [ ] Write round-trip tests: parse → print → re-parse produces same AST

---

## Phase 4 — Catalog & Data Types

- [ ] Define `DataType` enum: `TypeInt`, `TypeFloat`, `TypeText`, `TypeBool`, `TypeNull`
- [ ] Implement type parsing from string: `"INT"/"INTEGER"` → TypeInt, etc.
- [ ] Implement `Value` struct with union-style fields + IsNull
- [ ] Implement `Value` comparison: `Compare(a, b Value) (int, error)` returning -1/0/1
- [ ] Implement `Value` arithmetic: `Add`, `Sub`, `Mul`, `Div`
- [ ] Implement `Value.String()` for display
- [ ] Implement `Catalog` with `tables` map
- [ ] Implement `Table` struct with column slice
- [ ] Implement `Column` struct (name, type, nullable, pk, ordinal)
- [ ] Implement `Catalog.Register(table)` — panics on duplicate
- [ ] Implement `Catalog.Lookup(name)` — case-insensitive
- [ ] Implement `Catalog.List()` — sorted table names
- [ ] Write catalog tests

---

## Phase 5 — Storage Engine

- [ ] Implement `Tuple` struct wrapping `[]Value`
- [ ] Implement `HeapTable` with mutex-protected `[]Tuple`
- [ ] Implement `HeapTable.Insert(tuple)`
- [ ] Implement `HeapTable.Scan() []Tuple` (returns copy)
- [ ] Implement `HeapTable.RowCount() int64`
- [ ] Implement `Storage` registry (map from table name to `*HeapTable`)
- [ ] Implement `Storage.CreateTable(name)`, `Storage.GetTable(name)`
- [ ] Write storage tests

---

## Phase 6 — Seed Data

- [ ] Implement seed function that creates customers, products, orders tables in both catalog and storage
- [ ] Customers: 100 rows with realistic names, emails, 5 countries
- [ ] Products: 50 rows with 5 categories, prices 1-500
- [ ] Orders: 1000 rows with foreign keys, status distribution 60/20/10/10
- [ ] Verify seed data is consistent (all FK references valid)
- [ ] Add `POST /api/schema/seed` endpoint that resets and re-seeds

---

## Phase 7 — Semantic Analyzer

- [ ] Define `Scope` struct: maps alias → Table reference
- [ ] Implement `Analyzer` struct with catalog reference
- [ ] Implement `Analyze(stmt) error`
- [ ] For SELECT: resolve FROM clause, populate scope with table aliases
- [ ] Resolve JOIN clauses: add right-side tables to scope, validate ON condition columns
- [ ] Resolve column references in WHERE: look up in scope, handle qualified `t.col` and bare `col`
- [ ] Detect ambiguous bare column references (same name in multiple tables)
- [ ] Validate aggregate functions are not in WHERE clause
- [ ] Validate GROUP BY columns reference correct scope
- [ ] Validate HAVING references only GROUP BY cols or aggregates
- [ ] Expand `SELECT *` into explicit column list using scope
- [ ] Propagate resolved column type annotations onto ColumnRef nodes
- [ ] Validate CREATE TABLE: no duplicate column names, valid types
- [ ] Validate INSERT: column count matches value count, type compatibility
- [ ] Write analyzer tests: valid queries, each error case

---

## Phase 8 — Logical Planner

- [ ] Define `LogicalPlan` interface: `Children() []LogicalPlan`, `Schema() []Column`, `String() string`
- [ ] Implement all logical nodes: `LogicalScan`, `LogicalFilter`, `LogicalProject`, `LogicalJoin`, `LogicalAggregate`, `LogicalSort`, `LogicalLimit`
- [ ] Implement `Schema()` on each node (propagates columns upward)
- [ ] Implement `LogicalPlanBuilder.Build(stmt) LogicalPlan`
- [ ] Build order: Scan → Join → Filter (WHERE) → Aggregate (GROUP BY/HAVING) → Project (SELECT exprs) → Sort (ORDER BY) → Limit
- [ ] Handle subqueries: wrap in `LogicalSubquery` node
- [ ] Handle HAVING: as a Filter node above Aggregate
- [ ] Implement `LogicalPlanPrinter` (indented tree string for debugging)
- [ ] Implement `LogicalPlan.ToJSON()` for API responses
- [ ] Write planner tests: all query types produce correct tree structure

---

## Phase 9 — Rule-Based Optimizer

- [ ] Define `Rule` interface: `Name() string`, `Apply(plan) (plan, changed bool)`
- [ ] Implement rule application engine: loop until fixed point (or max 10 iterations)
- [ ] Implement `PredicatePushdown`:
  - [ ] Split conjunctive predicates (AND) into individual filters
  - [ ] Push filters through Project nodes (if projected columns are all present below)
  - [ ] Push filters through Join nodes (push to whichever side provides all referenced tables)
  - [ ] Do not push across Aggregation
- [ ] Implement `ProjectionPushdown`:
  - [ ] Walk plan from top, collect required columns at each node
  - [ ] Insert `LogicalProject` nodes to drop unreferenced columns below joins
- [ ] Implement `ConstantFolding`:
  - [ ] Evaluate binary expressions where both sides are literals
  - [ ] Simplify `x AND TRUE → x`, `x OR FALSE → x`, `NOT NOT x → x`
  - [ ] Evaluate comparisons between literals: `3 > 2 → TRUE`
- [ ] Implement `EliminateDeadFilter`:
  - [ ] Replace `Filter(TRUE, child)` with `child`
  - [ ] Replace `Filter(FALSE, child)` with `EmptyRelation`
- [ ] Write optimizer tests: each rule fires correctly, no infinite loops, idempotent (applying twice = applying once)

---

## Phase 10 — Statistics & Cost Model

- [ ] Implement `TableStats` and `ColumnStats` structs
- [ ] Implement `Histogram` with N equi-depth buckets
- [ ] Implement `StatsCollector.Collect(heapTable, catalog) TableStats`:
  - [ ] Count rows
  - [ ] Per column: count distinct, count nulls, find min/max, build histogram
- [ ] Collect stats during seed data load
- [ ] Implement `CardinalityEstimator`:
  - [ ] `EstimateScan(table) int64`
  - [ ] `EstimateFilter(child, predicate) int64`: equality, range, LIKE, IN
  - [ ] `EstimateJoin(left, right, condition) int64`
  - [ ] `EstimateAggregate(child, groupBy) int64`
- [ ] Implement `CostModel`:
  - [ ] `CostSeqScan(rows, pages) float64`
  - [ ] `CostHashJoin(outerRows, innerRows) float64`
  - [ ] `CostNLJoin(outerRows, innerRows) float64`
  - [ ] `CostSortMergeJoin(outerRows, innerRows) float64`
  - [ ] `CostSort(rows) float64`
  - [ ] `CostHashAgg(rows) float64`
- [ ] Write cost model tests with known inputs/outputs

---

## Phase 11 — Join Order Optimizer (CBO)

- [ ] Implement `JoinOrderOptimizer.Optimize(joins []LogicalJoin) LogicalPlan`
- [ ] Extract join relations and their conditions (join graph)
- [ ] Implement DP enumerate: for n ≤ 10 relations, enumerate 2^n subsets
- [ ] For each subset of size ≥ 2: try all left/right splits that are connected by a join condition
- [ ] For each split: recursively get best plan for left subset, right subset
- [ ] Estimate cardinality and cost of joining them
- [ ] Memoize best plan per subset
- [ ] Return best overall plan as left-deep tree
- [ ] Cap at 10 tables; fall back to left-deep original order for > 10
- [ ] Write join order tests: 2-table, 3-table, verify optimal order chosen

---

## Phase 12 — Physical Planner

- [ ] Define `PhysicalPlan` interface (same as logical: Children, Schema, String)
- [ ] Implement `PhysicalPlanBuilder.Build(logical LogicalPlan) PhysicalPlan`
- [ ] Map `LogicalScan` → `PhysicalSeqScan`
- [ ] Map `LogicalFilter` → `PhysicalFilter`
- [ ] Map `LogicalProject` → `PhysicalProjection`
- [ ] Map `LogicalJoin` → best of `PhysicalHashJoin`, `PhysicalNLJoin`, `PhysicalSortMergeJoin` using cost model
- [ ] Map `LogicalAggregate` → `PhysicalHashAgg` (default) or `PhysicalSortAgg` (if input sorted on group-by cols)
- [ ] Map `LogicalSort` → `PhysicalSort`
- [ ] Map `LogicalLimit` → `PhysicalLimit`
- [ ] Annotate physical nodes with estimated rows and cost
- [ ] Implement `PhysicalPlan.ToJSON()` for API responses
- [ ] Write physical planner tests

---

## Phase 13 — Executor (Operators)

- [ ] Define `Operator` interface: `Open(*ExecContext)`, `Next() (*Tuple, error)`, `Close()`, `Schema() []Column`
- [ ] Define `ExecContext` struct: stats counters (rows scanned, rows emitted per operator)
- [ ] Implement `SeqScan`: open = load all rows from storage, next = iterate, close = clear
- [ ] Implement `Filter`: delegates to child, evaluates predicate expression
- [ ] Implement `Projection`: delegates to child, evaluates projection expressions, produces new tuple
- [ ] Implement expression evaluator `Eval(expr, tuple, schema) (Value, error)`:
  - [ ] Literals: return value directly
  - [ ] ColumnRef: look up by column name in schema, index into tuple
  - [ ] BinaryExpr: evaluate both sides, apply operator
  - [ ] UnaryExpr: NOT, unary minus
  - [ ] FunctionCall: COUNT(*), SUM, AVG, MIN, MAX (aggregate markers — actual state in HashAgg)
  - [ ] CASE/WHEN/THEN/ELSE: evaluate conditions in order
  - [ ] IS NULL / IS NOT NULL
  - [ ] IN (list): evaluate each list element
  - [ ] BETWEEN: evaluate low/high comparisons
  - [ ] LIKE: convert SQL pattern to regex (% → .*, _ → .)
- [ ] Implement `NestedLoopJoin`:
  - [ ] Open: open left and right children
  - [ ] Next: advance outer; for each outer, scan all inner; filter by join condition; emit match
  - [ ] Handle LEFT join: emit outer tuple with NULLs if no match found
- [ ] Implement `HashJoin`:
  - [ ] Open: build phase — consume entire inner side, hash on join key, store in map
  - [ ] Next: probe phase — stream outer side, probe map, emit matches
  - [ ] Handle LEFT join: track which outer tuples produced no match, emit with NULLs at EOF
- [ ] Implement `SortMergeJoin`:
  - [ ] Open: consume both children into slices, sort both on join key
  - [ ] Next: two-pointer merge
  - [ ] Handle equal-key groups (multiple rows with same key on either side)
- [ ] Implement `HashAggregate`:
  - [ ] Open: consume entire child, group into `map[groupKey]aggState`
  - [ ] Aggregate state: per function type, accumulate values
  - [ ] Next: iterate over map, finalize aggregates (AVG = sum/count), emit
  - [ ] Handle `COUNT(*)` vs `COUNT(expr)` (null-skipping for expr)
- [ ] Implement `Sort`:
  - [ ] Open: consume all child tuples into slice
  - [ ] Apply sort using sort specs (multi-column, asc/desc)
  - [ ] Next: iterate sorted slice
- [ ] Implement `Limit`:
  - [ ] Open: open child, skip OFFSET tuples
  - [ ] Next: return up to N more tuples, then return nil
- [ ] Implement `Executor.Execute(plan PhysicalPlan) (*Result, error)`:
  - [ ] Create ExecContext
  - [ ] Open root operator
  - [ ] Loop Next() until nil, collect tuples
  - [ ] Close root operator
  - [ ] Return Result{Columns, Rows, Stats}
- [ ] Write executor integration tests: all operator types, all join types
- [ ] Write end-to-end tests: run all 5 sample queries, verify row counts and values

---

## Phase 14 — HTTP API

- [ ] Set up `chi` router with CORS middleware (allow `localhost:5173`)
- [ ] Implement logging middleware (method, path, duration, status)
- [ ] Implement panic recovery middleware
- [ ] Define all DTO structs in `api/dto.go` (JSON serializable)
- [ ] Implement `POST /api/query` handler:
  - [ ] Parse request body
  - [ ] Run full pipeline: lex → parse → analyze → logical plan → RBO → CBO → physical plan → execute
  - [ ] Capture optimization steps (rule name, changed bool, description) during RBO
  - [ ] Return full response DTO
  - [ ] Map `QueryError` to 400 with line/col info
- [ ] Implement `GET /api/schema` handler
- [ ] Implement `GET /api/stats` handler
- [ ] Implement `POST /api/explain` handler (no execution, just plans)
- [ ] Implement `POST /api/schema/table` (CREATE TABLE)
- [ ] Implement `POST /api/schema/seed` (reset + reseed)
- [ ] Write API integration tests using `httptest`

---

## Phase 15 — Frontend: Core Infrastructure

- [ ] Set up React Router with routes: `/` (Playground), `/schema`, `/stats`
- [ ] Implement API client in `src/api/client.ts` using `ky`
- [ ] Define TypeScript types in `src/types/index.ts` mirroring all backend DTOs
- [ ] Implement Zustand store: `sql`, `result`, `isLoading`, `error`, `plans`, `optimizationSteps`
- [ ] Implement `Nav` component with links to all pages, active state
- [ ] Set up global error boundary
- [ ] Implement `LoadingSpinner` and `ErrorAlert` shared components

---

## Phase 16 — Frontend: Playground Page

- [ ] Implement `SQLEditor` wrapping Monaco with:
  - [ ] SQL syntax highlighting (use built-in Monaco SQL mode)
  - [ ] Custom completion provider: table names, column names from schema API
  - [ ] Ctrl+Enter keybinding that calls execute
  - [ ] Error marker support (place red squiggles from `QueryError.line/col`)
  - [ ] Min height 200px, resizable
- [ ] Implement `Toolbar` with: Run button (with loading state), Clear button, sample queries dropdown (5 presets)
- [ ] Implement `ResultTable` with:
  - [ ] Column headers
  - [ ] Virtualized rows (react-virtual or simple pagination, 50 rows/page)
  - [ ] Row count badge
  - [ ] Execution time display
  - [ ] Export to CSV button
- [ ] Implement `ExecutionStats` bar: rows scanned, joins performed, sort ops
- [ ] Implement 3-panel layout with `react-resizable-panels`
- [ ] Implement `PlanViewer` tabs: Logical / Optimized / Physical
- [ ] Implement `PlanTree` using React Flow:
  - [ ] Convert plan JSON to React Flow nodes + edges (recursive tree walk)
  - [ ] Apply `dagre` auto-layout (top-down, node size 180×80)
  - [ ] Custom node component `PlanNode` showing: operator type, estimated rows, cost
  - [ ] Color nodes by type (scan=blue, join=amber, agg=purple, sort=teal, filter=gray)
  - [ ] Fit view on plan load
  - [ ] Zoom/pan controls
- [ ] Implement `PlanNode` detail sidebar (Sheet from shadcn): click node → shows all attributes
- [ ] Implement `OptimizationDiff` panel:
  - [ ] Show list of optimization steps (rule name, applied/skipped, description)
  - [ ] Highlight nodes in plan tree that were modified (different background)
- [ ] Implement `CostBar` component: relative cost bar normalized to max cost in tree

---

## Phase 17 — Frontend: Schema & Stats Pages

- [ ] Implement `SchemaPage`:
  - [ ] Fetch `/api/schema` on load
  - [ ] Sidebar table list with row count badges
  - [ ] Click table → show column details table (name, type, nullable, PK)
  - [ ] Mini INSERT editor per table (optional, nice-to-have)
- [ ] Implement `StatsPage`:
  - [ ] Fetch `/api/stats` on load
  - [ ] Per-table card: row count, page count
  - [ ] Per-column row: distinct count, null %, min, max
  - [ ] Histogram visualization using Recharts BarChart for numeric columns
  - [ ] Refresh button

---

## Phase 18 — Integration & Polish

- [ ] End-to-end smoke test: start backend, open frontend, run all 5 preset queries
- [ ] Verify plan tree renders for all 5 queries
- [ ] Verify optimization steps list is non-empty for join queries
- [ ] Verify statistics page loads all tables
- [ ] Add Monaco SQL completion provider (table/column names)
- [ ] Handle empty result sets gracefully in UI
- [ ] Handle query errors: show error in editor (squiggle + inline message below editor)
- [ ] Add responsive layout handling (min-width 1024px, graceful on smaller screens)
- [ ] Add loading skeletons for plan tree and result table
- [ ] Write README.md with: setup instructions, architecture overview, screenshots
- [ ] Write `Makefile` final targets: `make dev` (starts both), `make build`, `make test`

---

## Phase 19 — Stretch Goals (bonus features)

- [ ] `EXPLAIN ANALYZE` mode: actual row counts vs estimated (shows estimation accuracy)
- [ ] Query history: last 20 queries stored in localStorage, accessible from editor
- [ ] Dark mode support in frontend
- [ ] Index simulation: declare indexes in schema, planner can choose index scan with cost advantage
- [ ] UNION / INTERSECT / EXCEPT support in parser and planner
- [ ] Window functions: `ROW_NUMBER()`, `RANK()` over partitions
- [ ] Export query plan as PNG (React Flow has built-in support)
- [ ] Animated execution: step through plan tree showing data flow

---

## Testing Requirements

Each phase must pass its tests before moving to the next.

| Phase | Minimum test coverage |
|---|---|
| Lexer | All token types, edge cases |
| Parser | All grammar productions, precedence, errors |
| Analyzer | Valid queries + each error type |
| Logical Planner | Tree structure for all query shapes |
| RBO | Each rule fires + idempotency |
| CBO | Cost estimates are monotonically reasonable |
| Executor | All operators with known inputs/outputs |
| API | All endpoints with httptest |

Run all tests: `go test ./...`