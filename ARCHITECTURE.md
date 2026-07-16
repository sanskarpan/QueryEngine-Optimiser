# Architecture Guide

This document explains *why* the system is designed the way it is. Read this alongside SPEC.md to understand the design decisions, data flow contracts between components, and how real databases solve the same problems.

---

## The Big Picture: How a Query Moves Through the System

```
User types:  SELECT c.name, SUM(o.amount)
             FROM orders o JOIN customers c ON o.customer_id = c.id
             WHERE o.status = 'shipped'
             GROUP BY c.name
             ORDER BY 2 DESC LIMIT 5
```

Here is what happens at each stage:

### Stage 1: Lexer → Token Stream

```
[SELECT] [IDENT:c] [DOT] [IDENT:name] [COMMA] [IDENT:SUM] [LPAREN] ...
[FROM] [IDENT:orders] [IDENT:o] [JOIN] [IDENT:customers] [IDENT:c] ...
[ON] [IDENT:o] [DOT] [IDENT:customer_id] [EQ] [IDENT:c] [DOT] [IDENT:id]
...
```

No semantics yet. Pure tokenization. The lexer only knows characters.

### Stage 2: Parser → AST

```
SelectStatement {
  Columns: [AliasExpr{ColumnRef{table:"c",col:"name"}, ""},
            AliasExpr{FunctionCall{name:"SUM", args:[ColumnRef{table:"o",col:"amount"}]}, ""}]
  From:    TableRef{name:"orders", alias:"o"}
  Joins:   [JoinClause{type:INNER, table:TableRef{name:"customers",alias:"c"},
                       on:BinaryExpr{ColumnRef{o,customer_id}, EQ, ColumnRef{c,id}}}]
  Where:   BinaryExpr{ColumnRef{o,status}, EQ, StringLit{"shipped"}}
  GroupBy: [ColumnRef{table:"c", col:"name"}]
  OrderBy: [SortSpec{expr:IntLit{2}, dir:DESC}]
  Limit:   IntLit{5}
}
```

Still no catalog lookups. Just syntactic structure.

### Stage 3: Analyzer → Annotated AST

The analyzer resolves names against the catalog:
- `c.name` → `customers.name` column, type TEXT, ordinal 1
- `o.amount` → `orders.amount` column, type FLOAT, ordinal 4
- `o.status` → `orders.status` column, type TEXT, ordinal 3
- `o.customer_id` → `orders.customer_id` column, type INT, ordinal 2
- `c.id` → `customers.id` column, type INT, ordinal 0
- `ORDER BY 2` → resolves positional reference to `SUM(o.amount)`
- Validates: SUM is not used in WHERE, GROUP BY column is valid

### Stage 4: Logical Planner → Logical Plan Tree

```
LogicalLimit(count=5, offset=0)
  LogicalSort(by=[col:1 DESC])
    LogicalProject([c.name, SUM(o.amount)])
      LogicalAggregate(groupBy=[c.name], aggs=[SUM(o.amount)])
        LogicalFilter(o.status = 'shipped')
          LogicalJoin(INNER, on: o.customer_id = c.id)
            LogicalScan(orders, alias=o)
            LogicalScan(customers, alias=c)
```

Notice WHERE becomes a Filter above the Join. The optimizer will move it down.

### Stage 5: Rule-Based Optimizer (RBO)

`PredicatePushdown` fires:
- `o.status = 'shipped'` only references table `o` (orders)
- It can be pushed below the Join, to the left (orders) side
- Result: Filter moves below Join to become part of the orders scan

```
LogicalLimit(count=5)
  LogicalSort(by=[col:1 DESC])
    LogicalProject([c.name, SUM(o.amount)])
      LogicalAggregate(groupBy=[c.name], aggs=[SUM(o.amount)])
        LogicalJoin(INNER, on: o.customer_id = c.id)
          LogicalFilter(o.status = 'shipped')   ← PUSHED DOWN
            LogicalScan(orders, alias=o)
          LogicalScan(customers, alias=c)
```

Why does this matter? In the original plan, the join produces all rows first, then filters. In the optimized plan, orders is filtered to ~600 rows (60% shipped) before joining, reducing join cost by 40%.

`ProjectionPushdown` fires:
- Above the Join, only `c.name`, `o.amount`, and `o.customer_id` / `c.id` are needed
- Insert a narrow projection below the join on each side to drop unused columns
- This reduces tuple width flowing through the join, saving memory

### Stage 6: Cost-Based Optimizer (CBO) — Join Reordering

For this 2-table query, join order DP produces two candidates:

| Plan | Left | Right | Estimated cost |
|---|---|---|---|
| Option A | orders (600 filtered) | customers (100) | Build 100 rows, probe 600 = hash cost: 750 |
| Option B | customers (100) | orders (600 filtered) | Build 600 rows, probe 100 = hash cost: 1,000 |

CBO picks Option A: build the smaller side (customers), probe with the larger (filtered orders). This is the classic "build on smaller side" optimization.

### Stage 7: Physical Planner → Physical Plan

Cost comparison for join selection:
- `NLJoin`: 600 × 100 × 0.01 = 600
- `HashJoin`: 1.5×100 + 1.0×600 = 750
- `SortMergeJoin`: sort(600) + sort(100) + 700 ≈ 1400

Surprisingly, NLJ wins here because the tables are small. For larger tables (100k × 10k), HashJoin would dominate.

```
PhysicalLimit(5)
  PhysicalSort([col:1 DESC])
    PhysicalProjection([c.name, SUM(o.amount)])
      PhysicalHashAgg(groupBy=[c.name], aggs=[SUM(o.amount)])
        PhysicalNLJoin(INNER, on: o.customer_id = c.id)
          PhysicalFilter(o.status = 'shipped')
            PhysicalSeqScan(orders)
          PhysicalSeqScan(customers)
```

### Stage 8: Executor (Volcano Model)

The Volcano model is pull-based: the root operator calls `Next()` on its child, which calls `Next()` on its child, and so on. Tuples flow upward one at a time.

Execution trace (simplified):
```
Limit.Next()
  Sort.Next()      ← Sort must buffer ALL tuples before it can return first
    ...            ← Sort calls Next() repeatedly until EOF, builds sorted array
      NLJoin.Next()
        left.Next()  → orders: returns first row where status='shipped'
        right.Open() → full scan of customers (100 rows loaded)
        right.Next() → customers[0]: does o.customer_id = c.id? no → skip
        right.Next() → customers[1]: match! → emit joined tuple
        ...
```

**Blocking operators break the pipeline.** Sort, HashAggregate, and HashJoin (build phase) must see all input before producing output. In a real database, these would spill to disk (grace hash join, external sort). We don't implement spilling — we're in-memory only.

---

## Design Decisions

### Why hand-written recursive descent parser (not yacc)?

1. **Better error messages.** Generated parsers produce cryptic "syntax error at token X" messages. A hand-written parser can say "expected table name after FROM, got WHERE at line 3 col 8."
2. **Full control over AST.** Generated parsers produce parse trees tied to the grammar. We want our own clean AST types.
3. **Educational value.** The whole point of this project is to understand how query engines work. Using a parser generator skips the most instructive part.
4. **SQL isn't that complex.** SQL has known, stable grammar. It's not worth the yacc/antlr dependency.

### Why Volcano/Iterator model (not vectorized)?

The Volcano model (one tuple at a time) is simpler to implement correctly than vectorized execution (batches of tuples). Modern databases (DuckDB, Velox) use vectorized execution for 10-100x performance, but the code is significantly more complex. For understanding query engines, Volcano is the right teaching model.

A vectorized executor would change `Next() *Tuple` to `Next() []Tuple` (returning a batch of, say, 1024 tuples at a time), which dramatically improves CPU cache utilization and enables SIMD.

### Why no disk storage?

Disk storage requires implementing:
- Buffer pool manager (cache pages in memory)
- Page layout (slotted pages, free space management)
- B-tree for indexes
- Write-ahead log for durability

Each of these is a significant project by itself. Our goal is the query processing pipeline, not the storage engine. The in-memory heap store gives us correct semantics with minimal complexity.

### Why dynamic programming for join reordering (not greedy)?

A greedy algorithm (always join the two cheapest relations next) is fast but suboptimal. Consider three tables A, B, C where:
- A JOIN B → 10,000 rows
- A JOIN C → 50 rows  
- (A JOIN C) JOIN B → 500 rows (much better than (A JOIN B) JOIN C → 1,000,000 rows)

Greedy picks (A JOIN C) first only if it inspects all pairs. But if we add a 4th table D where B JOIN D is cheap, the interaction becomes non-local and greedy fails.

DP guarantees the optimal left-deep plan. It's O(3^n) in the worst case but for n ≤ 10 (which is our cap) this is at most ~59,000 subsets — completely tractable.

### Why equi-depth histograms (not equi-width)?

Equi-width histograms divide the value range into equal buckets. Problem: skewed data puts most values in a few buckets, making estimates poor for other buckets.

Equi-depth histograms put approximately equal numbers of values in each bucket. This gives much better selectivity estimates for skewed distributions (e.g., most orders have status='shipped', few have 'cancelled').

With 10 buckets, we can estimate `WHERE status = 'shipped'` as ~60% (matching our actual seed distribution), while equi-width histograms might estimate 25% (assuming uniform distribution across 4 statuses).

---

## Component Interaction Contracts

### Lexer ↔ Parser
- Parser calls `lexer.Next()` to advance and `lexer.Peek()` to look ahead without consuming
- Parser is responsible for all error reporting; lexer only returns `ILLEGAL` tokens
- Lexer is stateful (not re-entrant); one lexer per parse

### Parser ↔ Analyzer  
- Parser outputs AST nodes that MAY have unresolved names (string identifiers)
- Analyzer MUST NOT modify AST nodes in place; it annotates by adding resolved info
- If analysis fails, the original AST is discarded; only annotated AST proceeds

### Analyzer ↔ Logical Planner
- Logical planner receives fully-annotated AST where every ColumnRef has resolved table/column info
- Planner trusts the annotated AST; no further name resolution needed
- Planner uses catalog for schema propagation (column types for Schema())

### Logical Plan ↔ Optimizer
- Optimizer receives a logical plan tree and returns a (potentially new) logical plan tree
- The optimizer MUST NOT change the semantic meaning of the plan (same results, different structure)
- The optimizer captures its changes in an `[]OptimizationStep` slice passed by reference

### Logical Plan ↔ Physical Planner
- Physical planner receives optimized logical plan, returns physical plan
- Physical nodes carry `EstimatedRows int64` and `EstimatedCost float64` from the cost model
- Physical planner uses the cost model to choose algorithms; it doesn't re-run statistics

### Physical Plan ↔ Executor
- Executor's `Execute(plan)` function builds the operator tree bottom-up (mirroring the plan tree)
- Each `PhysicalNode` has a corresponding `Operator` implementation
- The operator tree is built once, opened once, drained, closed once

---

## Schema Flow Through the System

One of the trickier aspects is tracking which columns are available at each stage. Here is how schemas flow:

```
Catalog: orders = [id INT, customer_id INT, product_id INT, amount FLOAT, status TEXT]
         customers = [id INT, name TEXT, email TEXT]

LogicalScan(orders, alias=o)  .Schema() = [o.id, o.customer_id, o.product_id, o.amount, o.status]
LogicalScan(customers, alias=c) .Schema() = [c.id, c.name, c.email]

LogicalJoin(o, c)  .Schema() = [o.id, o.customer_id, o.product_id, o.amount, o.status, c.id, c.name, c.email]

LogicalFilter(join, o.status='shipped')  .Schema() = same as join (filter doesn't change schema)

LogicalAggregate(filter, groupBy=[c.name], aggs=[SUM(o.amount) AS sum_amount])
  .Schema() = [c.name, sum_amount FLOAT]

LogicalProject(agg, [c.name, sum_amount])
  .Schema() = [c.name, sum_amount]  ← same here, but might rename/add expressions

LogicalSort(project, [sum_amount DESC])  .Schema() = [c.name, sum_amount]
LogicalLimit(sort, 5)  .Schema() = [c.name, sum_amount]
```

The executor uses these schemas to index into tuples by column name. When `Filter` evaluates `o.status = 'shipped'`, it calls `schema.IndexOf("o.status")` to get column position 4 in the tuple, then `tuple.Values[4]` to get the value.

---

## Optimization Deep Dive

### Predicate Pushdown: Why It's the Most Important Rule

Consider `WHERE o.status = 'shipped'` applied after a join of 1000 orders × 100 customers = 100,000 intermediate rows. After pushdown, we filter 1000 orders to 600 before joining: 600 × 100 = 60,000 intermediate rows. That's 40% fewer rows through the join.

For a 3-way join (orders × customers × products), the difference is even more dramatic:
- Without pushdown: 1000 × 100 × 50 = 5,000,000 intermediate rows
- With pushdown (filter orders before joining): 600 × 100 × 50 = 3,000,000

And if we have a filter on products too: 600 × 100 × 10 = 600,000. Pushing both predicates down gives 8x improvement.

### Projection Pushdown: Reducing Tuple Width

After a 3-way join, a tuple might have 15 columns. If we only need 3 in the final result, carrying 15 columns through every subsequent operator wastes memory bandwidth. Projection pushdown inserts a narrowing Project node below each join:

```
Before:
  HashJoin(orders×customers) → 8-column tuples → SortMergeJoin(×products) → 15-column tuples → Sort → Limit

After:
  HashJoin(Project(orders,[id,customer_id,amount,status]) × Project(customers,[id,name]))
    → 5-column tuples
  → SortMergeJoin(5-col × Project(products,[id,name]))
    → 3-column tuples
  → Sort → Limit
```

Each tuple is now 3-5 values instead of 15, reducing memory pressure by 5x.

### Constant Folding

`WHERE amount > 100 + 50` is evaluated at plan time to `WHERE amount > 150`. This eliminates a redundant addition for every tuple scanned. For a table with 1M rows, that's 1M fewer additions.

More impactful: `WHERE 1 = 1` becomes `WHERE true` which eliminates the filter node entirely. `WHERE 1 = 2` becomes `WHERE false` which eliminates the entire subtree (returns empty relation immediately).

---

## Join Algorithm Selection Guide

The physical planner picks the join algorithm by comparing costs. Here is the intuition:

### Nested Loop Join (NLJ)
```
for each outer_row in left:
    for each inner_row in right:
        if condition(outer_row, inner_row): emit
```
- Cost: O(n × m)
- Good when: one side is very small (< 10 rows), or there's no equi-join condition
- Memory: O(1) — no buffering needed
- In this system: fastest for tiny tables

### Hash Join
```
// Build phase
for each inner_row in right:
    hash_map[key(inner_row)].append(inner_row)

// Probe phase  
for each outer_row in left:
    for match in hash_map[key(outer_row)]:
        emit (outer_row, match)
```
- Cost: O(n + m) — linear in both inputs
- Good when: large tables with an equi-join condition
- Memory: O(m) — must buffer entire build side
- In this system: best for large tables
- Build on smaller side always (reduces peak memory and build time)

### Sort-Merge Join
```
sort left by join key
sort right by join key
two-pointer merge: advance both pointers together
```
- Cost: O(n log n + m log m)
- Good when: inputs are already sorted on join key (sort cost amortized)
- Memory: O(n + m) — must buffer and sort both sides
- In this system: competitive when multiple joins share the same key (sort once, reuse)

---

## Statistics and Selectivity Estimation

The cost model is only as good as its cardinality estimates. Bad estimates → wrong join order → slow queries.

### Equality selectivity: `column = value`
```
selectivity = 1 / distinctCount(column)
estimated_rows = base_rows × selectivity
```

Example: `orders.status = 'shipped'` with 4 distinct statuses:
- Naive estimate: 1000 / 4 = 250 rows
- With histogram: bucket for 'shipped' has frequency 600 → estimate 600 rows

The histogram gives a much better estimate because we know the distribution is skewed.

### Range selectivity: `column BETWEEN low AND high`
Walk the histogram and sum frequencies of buckets that fall within [low, high]. For buckets that partially overlap, estimate proportionally:
```
overlap = (bucketHigh - low) / (bucketHigh - bucketLow)
contribution = bucketFrequency × overlap
```

### Join selectivity
```
estimated_rows = left.rows × right.rows / max(left.ndv(join_col), right.ndv(join_col))
```
This assumes values are uniformly distributed and each left value matches approximately `right.rows / right.ndv` rows on the right. It's an approximation — real databases use multi-column statistics and sampling for better estimates.

---

## Error Taxonomy

| Error Type | HTTP Status | When |
|---|---|---|
| `LexError{line, col, char}` | 400 | Unrecognized character |
| `ParseError{line, col, msg}` | 400 | Syntax violation |
| `AnalysisError{line, col, msg}` | 422 | Unknown table/column, type error |
| `PlanError{msg}` | 500 | Should not happen (bug in planner) |
| `ExecError{msg}` | 500 | Division by zero, type mismatch at runtime |

All errors include `stage` field so the frontend knows which phase failed.

Frontend behavior:
- `ParseError` with line/col → show red squiggle in Monaco editor at that position
- `AnalysisError` → show error message below editor, no squiggle
- `ExecError` → show in results area with "Execution failed" message

---

## Performance Expectations (in-memory, seed data)

| Query | Expected time | Dominant cost |
|---|---|---|
| `SELECT * FROM orders WHERE status = 'shipped'` | < 1ms | Sequential scan 1000 rows |
| `SELECT COUNT(*) FROM orders JOIN customers c ON ...` | < 5ms | Hash join, 1000 × 100 |
| 3-way join, GROUP BY, ORDER BY | < 20ms | Build phases + sort |
| Subquery with IN | < 10ms | Subquery evaluated once |

These are all well within acceptable range for an in-memory engine on seed data. The implementation does not need to be highly optimized — correctness and clarity are more important.

---

## Testing Philosophy

**Unit tests** test individual components in isolation with mock/stub dependencies:
- Lexer: input string → expected tokens
- Parser: input string → expected AST (compare with ASTPrinter output)
- Optimizer rules: input logical plan → expected transformed plan
- Cost model: input cardinalities → expected costs

**Integration tests** test component chains:
- Lexer + Parser: SQL string → AST (no mocks)
- Analyzer: SQL string + catalog → annotated AST or error
- Full pipeline: SQL string → result rows (no HTTP layer)

**API tests** use `httptest.NewServer`:
- Each endpoint: happy path + error cases
- Test all 5 preset queries end-to-end

**Invariant tests** verify system-level properties:
- RBO idempotency
- Schema propagation consistency
- Cardinality monotonicity

Never mock the catalog in integration tests — populate it with seed data and test against real tables. This catches bugs where the system works with mocks but fails with real data.