# Claude Code Master Prompt — Query Engine & Optimizer

You are building a **SQL Query Engine & Optimizer from scratch**. This is a serious systems engineering project. Read this entire prompt before writing a single line of code. Then read `SPEC.md` and `CHECKLIST.md`. Build in the exact order described.

---

## What You Are Building

A full-stack application that implements a complete SQL processing pipeline:

```
SQL string
    ↓ Lexer        → token stream
    ↓ Parser       → abstract syntax tree (AST)
    ↓ Analyzer     → annotated AST (types resolved, names bound)
    ↓ Logical Plan → relational algebra tree
    ↓ RBO          → rule-based optimized logical plan
    ↓ CBO          → cost-based join-reordered logical plan
    ↓ Physical Plan → concrete algorithm selections
    ↓ Executor     → result rows (Volcano iterator model)
```

The frontend is a React + TypeScript playground that shows the SQL editor, results, query plan visualizer, and optimization steps side by side.

---

## Non-Negotiable Rules

1. **Work in phase order.** CHECKLIST.md defines 19 phases. Do not start phase N+1 until phase N passes its tests.
2. **No external databases.** No PostgreSQL, SQLite, BoltDB, or any database driver. All storage is in-memory Go structs.
3. **No code generation.** Do not generate SQL parsers with yacc/antlr/goyacc. Write the recursive-descent parser by hand.
4. **Write tests as you go.** Each phase in CHECKLIST.md has test requirements. Write tests before moving on.
5. **Keep Go idiomatic.** Named return values for errors, explicit error propagation (no panics in library code), interfaces over concrete types.
6. **Keep TypeScript strict.** `"strict": true` in tsconfig. No `any` types except in the Monaco editor integration.

---

## Starting Instructions

```bash
# Create project root
mkdir query-engine && cd query-engine

# Read the specs first
cat SPEC.md
cat CHECKLIST.md

# Then begin Phase 0
```

---

## Phase-by-Phase Build Instructions

### Phase 0 — Scaffolding

```bash
# Backend
go mod init github.com/yourname/query-engine
go get github.com/go-chi/chi/v5
go get github.com/stretchr/testify/assert
go get github.com/stretchr/testify/require

# Create directory structure from SPEC.md
mkdir -p cmd/server internal/{lexer,parser,ast,catalog,analyzer,storage,stats}
mkdir -p internal/planner/{logical,physical}
mkdir -p internal/optimizer/{rule,cost}
mkdir -p internal/executor/operators
mkdir -p api testdata/queries

# Frontend
mkdir web && cd web
npm create vite@latest . -- --template react-ts
npm install tailwindcss @tailwindcss/vite @monaco-editor/react @xyflow/react @dagrejs/dagre recharts zustand ky react-resizable-panels
npm install @dagrejs/graphlib
npx shadcn@latest init
npx shadcn@latest add button badge table tabs sheet tooltip card separator scroll-area
```

Create `Makefile`:
```makefile
.PHONY: build test run lint dev

build:
	go build -o bin/server ./cmd/server

test:
	go test ./... -v -count=1

run:
	go run ./cmd/server

dev:
	make run & (cd web && npm run dev)

lint:
	go vet ./...
	cd web && npx tsc --noEmit
```

---

### Phase 1 — Lexer

Start with `internal/lexer/token.go`. Define the `TokenType` as `int` with iota. Group into: literals (INT_LIT, FLOAT_LIT, STRING_LIT, BOOL_LIT), keywords (SELECT, FROM, WHERE, ...), operators (EQ, NEQ, LT, ...), symbols (LPAREN, RPAREN, COMMA, DOT, SEMI, STAR), and meta (EOF, ILLEGAL, IDENT).

For the keyword map, use a `map[string]TokenType` with all keywords in uppercase. In `Next()`, after reading an identifier, check `strings.ToUpper(ident)` against the keyword map.

The `Token` struct:
```go
type Token struct {
    Type    TokenType
    Literal string   // raw text as it appeared in input
    Line    int
    Col     int
}
```

The `Lexer` struct tracks `input []rune` (not `[]byte` — handle unicode), `pos int`, `line int`, `col int`.

Key lexer implementation note: `Peek()` must not advance position. Implement it by saving pos/line/col, calling `Next()`, then restoring.

**Write these test cases first (test-driven):**
```go
func TestLexer_Keywords(t *testing.T) { ... }   // SELECT FROM WHERE etc.
func TestLexer_Operators(t *testing.T) { ... }  // = != < > <= >= || etc.
func TestLexer_StringLiteral(t *testing.T) { }  // 'hello', 'it''s fine'
func TestLexer_Numbers(t *testing.T) { }        // 42, 3.14, 0.5
func TestLexer_Position(t *testing.T) { }       // line/col tracking
func TestLexer_Comments(t *testing.T) { }       // -- and /* */
```

---

### Phase 2 & 3 — AST + Parser

The parser is the most complex component. Implement it as a recursive-descent parser with Pratt parsing for expressions.

**Critical: implement expression parsing with precedence correctly.** Use this binding power table:
```go
var precedence = map[TokenType]int{
    OR:      1,
    AND:     2,
    NOT:     3,    // prefix
    EQ:      4, NEQ: 4, LT: 4, GT: 4, LTE: 4, GTE: 4,
    LIKE:    4, IN: 4, BETWEEN: 4, IS: 4,
    PLUS:    5, MINUS: 5,
    STAR:    6, SLASH: 6, PERCENT: 6,
    // Unary minus and NOT handled at prefix level
}
```

Pratt parser structure:
```go
func (p *Parser) parseExpression(minPrec int) Expression {
    left := p.parseUnary()  // handles unary minus, NOT, EXISTS
    for {
        prec, ok := precedence[p.current.Type]
        if !ok || prec < minPrec { break }
        op := p.current
        p.advance()
        // handle postfix: IN, BETWEEN, IS NULL, LIKE
        if op.Type == IN { return p.parseIn(left) }
        if op.Type == BETWEEN { return p.parseBetween(left) }
        if op.Type == IS { return p.parseIsNull(left) }
        if op.Type == LIKE { right := p.parseExpression(prec + 1); return &BinaryExpr{...} }
        right := p.parseExpression(prec + 1)  // right-associative: use prec+1 for left-assoc
        left = &BinaryExpr{Left: left, Op: op, Right: right}
    }
    return left
}
```

**Parser tests must cover:**
- `SELECT 1+2*3` parses as `SELECT (1+(2*3))` — verify with ASTPrinter
- `SELECT a, b FROM t WHERE x > 1 AND y < 2` — full SELECT with WHERE
- `SELECT COUNT(*), SUM(amount) FROM orders GROUP BY customer_id HAVING COUNT(*) > 5`
- `SELECT t1.id, t2.name FROM t1 JOIN t2 ON t1.id = t2.fk LEFT JOIN t3 ON t2.id = t3.id`
- `SELECT * FROM t1 WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')`
- `SELECT *, (SELECT COUNT(*) FROM orders WHERE customer_id = c.id) AS order_count FROM customers c`
- `CREATE TABLE products (id INT PRIMARY KEY, name TEXT NOT NULL, price FLOAT)`
- Error: `SELECT FROM` (missing column list) → error with position

---

### Phase 4 — Catalog & Values

The `Value` type is used throughout the executor. It must handle null correctly:
```go
type Value struct {
    Type     DataType
    IsNull   bool
    IntVal   int64
    FloatVal float64
    StrVal   string
    BoolVal  bool
}

func (v Value) Compare(other Value) (int, error) {
    if v.IsNull || other.IsNull { return 0, ErrNullComparison }
    // type coercion: int op float → promote int to float
    ...
}
```

Numeric type coercion: when comparing INT vs FLOAT, promote INT to FLOAT. When adding INT + FLOAT, result is FLOAT.

---

### Phase 5 & 6 — Storage + Seed

The storage layer is intentionally simple. `HeapTable` is just a `[]Tuple` behind a `sync.RWMutex`. Scan returns a copy of the slice to avoid race conditions during iteration.

For seed data, generate it programmatically (not from a file). Use a deterministic PRNG (`math/rand` with fixed seed 42) so tests are reproducible. The seed data must produce:
- `customers`: 100 rows, 5 distinct countries (US, UK, DE, FR, JP)
- `products`: 50 rows, 5 categories, prices between 10.0 and 499.99
- `orders`: 1000 rows, valid customer_id (1-100) and product_id (1-50), status distribution 60/20/10/10

---

### Phase 7 — Analyzer

The analyzer's job is to take an AST that uses string names and produce a resolved AST where every ColumnRef knows which table it came from and what type it is.

Key data structure: `Scope` maps alias → `*catalog.Table`. For unqualified column references, search all tables in scope and error if the column appears in multiple tables (ambiguous) or none (undefined).

```go
type Scope struct {
    tables  map[string]*catalog.Table  // alias → table
    aliases map[string]string           // alias → real table name
}

func (s *Scope) ResolveColumn(name, tableAlias string) (*catalog.Column, string, error) {
    // if tableAlias given: look up table by alias, find column
    // if no tableAlias: search all tables, error on ambiguity
}
```

The analyzer annotates `ColumnRef` nodes with resolved table name and column index (ordinal), so the executor can do index-based tuple access.

---

### Phase 8 — Logical Planner

Build order matters for query semantics. The relational algebra tree should be built bottom-up:

```
FROM (scan)
  ↓ JOIN
  ↓ WHERE (filter)
  ↓ GROUP BY + HAVING (aggregate + filter)
  ↓ SELECT list (project)
  ↓ ORDER BY (sort)
  ↓ LIMIT/OFFSET (limit)
```

HAVING becomes a `LogicalFilter` node above `LogicalAggregate`.

Each `LogicalPlan` node's `Schema()` must correctly propagate column information upward:
- `LogicalScan.Schema()` → columns from catalog table (with alias prefix)
- `LogicalJoin.Schema()` → left.Schema() + right.Schema()
- `LogicalProject.Schema()` → only projected columns with their aliases
- `LogicalAggregate.Schema()` → group-by columns + aggregate result columns

---

### Phase 9 — Rule-Based Optimizer

Predicate pushdown is the most impactful rule. Implement it carefully:

```go
// Collect which tables a predicate references
func predicateTables(expr ast.Expression) map[string]bool { ... }

// A predicate can be pushed below a join if all its referenced tables
// are available on one side of the join
func canPushLeft(pred ast.Expression, leftTables map[string]bool) bool {
    for t := range predicateTables(pred) {
        if !leftTables[t] { return false }
    }
    return true
}
```

When splitting AND predicates for pushdown:
```go
func splitConjuncts(expr ast.Expression) []ast.Expression {
    if b, ok := expr.(*ast.BinaryExpr); ok && b.Op.Type == lexer.AND {
        return append(splitConjuncts(b.Left), splitConjuncts(b.Right)...)
    }
    return []ast.Expression{expr}
}
```

---

### Phase 10 & 11 — Cost Model + Join Order

The join order optimizer is a classic DP over subsets. Here is the skeleton:

```go
type JoinOrderOptimizer struct {
    relations []LogicalPlan          // base relations
    conditions []JoinCondition       // all available join predicates
    memo       map[uint64]LogicalPlan // subset bitmask → best plan
    estimator  *CardinalityEstimator
    costModel  *CostModel
}

func (j *JoinOrderOptimizer) Optimize() LogicalPlan {
    n := len(j.relations)
    // base case: single relation plans
    for i := 0; i < n; i++ {
        mask := uint64(1) << i
        j.memo[mask] = j.relations[i]
    }
    // build up from size 2 to n
    for size := 2; size <= n; size++ {
        for _, mask := range subsetsOfSize(n, size) {
            j.memo[mask] = j.bestPlan(mask)
        }
    }
    fullMask := (uint64(1) << n) - 1
    return j.memo[fullMask]
}

func (j *JoinOrderOptimizer) bestPlan(mask uint64) LogicalPlan {
    var best LogicalPlan
    bestCost := math.MaxFloat64
    // try all ways to split mask into left | right where both are non-empty
    for sub := (mask - 1) & mask; sub > 0; sub = (sub - 1) & mask {
        complement := mask ^ sub
        if complement == 0 { continue }
        // only consider splits where there is a join condition between sub and complement
        if !j.hasJoinCondition(sub, complement) { continue }
        left := j.memo[sub]
        right := j.memo[complement]
        if left == nil || right == nil { continue }
        plan := j.makeJoin(left, right, sub, complement)
        cost := j.costModel.CostPlan(plan)
        if cost < bestCost { bestCost = cost; best = plan }
    }
    return best
}
```

---

### Phase 12 — Physical Planner

For join algorithm selection, compare three costs and pick minimum:
```go
func (b *PhysicalPlanBuilder) selectJoinAlgorithm(
    left, right PhysicalPlan,
    join *logical.LogicalJoin,
) PhysicalPlan {
    nlCost := b.cost.CostNLJoin(left.EstimatedRows(), right.EstimatedRows())
    hashCost := b.cost.CostHashJoin(left.EstimatedRows(), right.EstimatedRows())
    smCost := b.cost.CostSortMergeJoin(left.EstimatedRows(), right.EstimatedRows())

    switch min3(nlCost, hashCost, smCost) {
    case nlCost:   return NewNLJoinOp(left, right, join)
    case hashCost: return NewHashJoinOp(left, right, join)
    default:       return NewSortMergeJoinOp(left, right, join)
    }
}
```

---

### Phase 13 — Executor

The Volcano model means each operator is lazy — it produces one tuple at a time on demand. This is important for memory efficiency (no need to materialize intermediate results for pipeline operators).

**Exceptions** (blocking operators that must materialize before producing output):
- `HashJoin` — must consume entire build side before probing
- `HashAggregate` — must consume all input before emitting groups
- `Sort` — must consume all input before sorting

**Expression evaluator critical cases:**
```go
// LIKE pattern matching
func likeToRegex(pattern string) *regexp.Regexp {
    // escape regex metacharacters in pattern first
    escaped := regexp.QuoteMeta(pattern)
    // then un-escape % and _ which are SQL wildcards
    r := strings.ReplaceAll(escaped, "%", ".*")
    r = strings.ReplaceAll(r, "_", ".")
    return regexp.MustCompile("^" + r + "$")
}

// NULL propagation: any arithmetic with NULL produces NULL
func evalBinary(op TokenType, left, right Value) Value {
    if left.IsNull || right.IsNull {
        return NullValue()  // NULL propagates
    }
    ...
}

// Exception: IS NULL and IS NOT NULL do NOT propagate NULL
func evalIsNull(expr Value, negated bool) Value {
    result := expr.IsNull
    if negated { result = !result }
    return BoolValue(result)
}
```

**HashJoin implementation note:** Use a composite hash key when there are multiple join conditions. The key is a string concatenation of value strings separated by a NUL byte (`\x00`).

---

### Phase 14 — HTTP API

Use `chi` for routing. The main handler function structure:

```go
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
    var req QueryRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, 400, "invalid request body", nil)
        return
    }

    // Run full pipeline, capturing each stage
    tokens, err := lexer.Tokenize(req.SQL)
    if err != nil { writeQueryError(w, err); return }

    ast, err := parser.Parse(tokens)
    if err != nil { writeQueryError(w, err); return }

    annotated, err := h.analyzer.Analyze(ast)
    if err != nil { writeQueryError(w, err); return }

    logical := h.logicalPlanner.Build(annotated)
    steps := []OptimizationStep{}
    optimized := h.optimizer.Optimize(logical, &steps)
    physical := h.physicalPlanner.Build(optimized)

    var result *executor.Result
    if !req.Options.ExplainOnly {
        result, err = h.executor.Execute(physical)
        if err != nil { writeServerError(w, err); return }
    }

    writeJSON(w, 200, buildResponse(logical, optimized, physical, result, steps))
}
```

CORS middleware must allow `Origin: http://localhost:5173` with methods GET, POST and header Content-Type.

---

### Phase 15-17 — Frontend

**Plan tree layout with dagre:**

```typescript
import dagre from '@dagrejs/dagre';

function layoutPlanTree(planNode: PlanNode): { nodes: Node[], edges: Edge[] } {
    const dagreGraph = new dagre.graphlib.Graph();
    dagreGraph.setDefaultEdgeLabel(() => ({}));
    dagreGraph.setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 80 });

    const rfNodes: Node[] = [];
    const rfEdges: Edge[] = [];

    function traverse(node: PlanNode, parentId?: string) {
        dagreGraph.setNode(node.id, { width: 200, height: 80 });
        rfNodes.push({
            id: node.id,
            type: 'planNode',
            data: node,
            position: { x: 0, y: 0 }, // overwritten by dagre
        });
        if (parentId) {
            rfEdges.push({ id: `${parentId}-${node.id}`, source: parentId, target: node.id });
            dagreGraph.setEdge(parentId, node.id);
        }
        node.children.forEach(child => traverse(child, node.id));
    }

    traverse(planNode);
    dagre.layout(dagreGraph);

    rfNodes.forEach(node => {
        const pos = dagreGraph.node(node.id);
        node.position = { x: pos.x - 100, y: pos.y - 40 };
    });

    return { nodes: rfNodes, edges: rfEdges };
}
```

**Monaco completion provider:**

```typescript
monaco.languages.registerCompletionItemProvider('sql', {
    provideCompletionItems: (model, position) => {
        const schema = useSchemaStore.getState().tables;
        const suggestions: monaco.languages.CompletionItem[] = [];

        // Add table names
        schema.forEach(table => {
            suggestions.push({
                label: table.name,
                kind: monaco.languages.CompletionItemKind.Class,
                insertText: table.name,
            });
            // Add column names
            table.columns.forEach(col => {
                suggestions.push({
                    label: `${table.name}.${col.name}`,
                    kind: monaco.languages.CompletionItemKind.Field,
                    insertText: `${table.name}.${col.name}`,
                    detail: col.type,
                });
            });
        });

        return { suggestions };
    },
});
```

---

## Key Correctness Invariants

These must hold for the system to be correct. Write assertion-based tests for each:

1. **Parser roundtrip:** `Print(Parse(sql))` re-parses to structurally identical AST.
2. **Schema propagation:** every logical plan node's `Schema()` exactly matches the columns it outputs.
3. **RBO idempotency:** `RBO(RBO(plan))` produces identical plan to `RBO(plan)`.
4. **Cardinality monotonicity:** filter cardinality ≤ scan cardinality for the same table.
5. **Executor correctness:** `SELECT COUNT(*) FROM orders` = 1000 (after seeding).
6. **NULL propagation:** `SELECT 1 + NULL FROM orders LIMIT 1` returns one row with NULL value.
7. **Join correctness:** `SELECT COUNT(*) FROM orders o JOIN customers c ON o.customer_id = c.id` = 1000 (all orders have valid customers in seed data).

---

## Common Pitfalls to Avoid

**Parser:**
- `SELECT *` is valid — the `*` is not a multiplication operator in SELECT list context. Check current token context.
- `COUNT(*)` — `*` inside function call is special, not a multiply. Parse it as a `StarExpr`.
- Column aliases that shadow table names: `SELECT id AS name FROM t WHERE name = 1` — `name` in WHERE refers to the column, not the alias. Aliases are only available in ORDER BY (and debatable in GROUP BY).

**Analyzer:**
- Correlated subqueries reference columns from outer scope — the inner query's analyzer needs access to the outer scope.
- `SELECT SUM(amount), customer_id FROM orders` without GROUP BY is an error. Detect: any aggregate + any non-aggregate column in SELECT without GROUP BY.

**Logical Planner:**
- HAVING is NOT the same as WHERE. WHERE is pushed below GROUP BY; HAVING filters aggregate results above GROUP BY.
- `SELECT a, COUNT(*) FROM t GROUP BY a ORDER BY COUNT(*)` — ORDER BY references the aggregate. Resolve it by matching the expression structure.

**Executor:**
- `SortMergeJoin` with multiple rows having identical join keys on both sides is O(n*m) in the worst case. Handle the inner group correctly.
- `HashAggregate` with no GROUP BY (e.g. `SELECT COUNT(*) FROM t`) must still produce exactly one output row even if input is empty (COUNT returns 0 for empty input, SUM returns NULL).
- Overflow: sum of large integers in aggregates — use int64 throughout.

**Optimizer:**
- Never push a predicate below an outer join if the predicate references the nullable side — it would incorrectly filter out null-extended rows.
- Join reordering must only reorder joins that are semantically commutative (INNER joins). Left/right outer joins cannot be freely reordered.

---

## File Naming Conventions

- Go files: `snake_case.go`
- Go test files: `snake_case_test.go`  
- TypeScript files: `PascalCase.tsx` for components, `camelCase.ts` for utilities
- One primary type/interface per Go file where reasonable
- Test files live alongside source files (not in a separate `test/` directory)

---

## Definition of Done

The project is complete when:

1. `go test ./...` passes with 0 failures
2. `cd web && npx tsc --noEmit` passes with 0 errors
3. All 5 preset queries execute correctly and return expected row counts
4. The plan visualizer renders correctly for all 5 queries
5. The optimization steps panel shows at least one rule firing for join queries
6. `make build` produces a working binary
7. The README explains how to run the project in 3 commands or fewer