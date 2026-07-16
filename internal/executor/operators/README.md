# Operators — Volcano Iterator Model

## Overview

This package implements query execution using the **Volcano** (also called **iterator** or **pipeline**) model. Every operator in the query plan implements a single interface:

```go
type Operator interface {
    Open(ctx *exectypes.ExecContext) error
    Next() (*exectypes.Tuple, error)
    Close() error
    Schema() []catalog.Column
}
```

Operators form a tree that mirrors the physical query plan. Each call to `Next()` on the root operator pulls one row through the entire tree on demand. This enables streaming evaluation: operators do not need to materialize their entire input before producing output.

## Lifecycle Contract

| Method | When called | Responsibility |
|--------|-------------|----------------|
| `Open` | Once, before first `Next` | Initialize state; open child operators |
| `Next` | Repeatedly until EOF | Return next row or `(nil, nil)` at end |
| `Close` | Once, after last `Next` | Release resources; close child operators |
| `Schema`| Any time after `Open` | Return output column definitions |

**Rules:**
- `Open` must be called before `Next` or `Schema`.
- `(nil, nil)` from `Next` signals end-of-input (EOF). The caller must stop.
- `(nil, err)` signals a runtime error. The caller must propagate the error.
- `Close` must always be called after a successful `Open`, even after errors from `Next`.
- If `Open` returns an error, the caller must **not** call `Next` or `Close`.
- If `Open` fails after opening children, it must close any already-opened children before returning.

## Operator Catalogue

| Operator | File | Description |
|----------|------|-------------|
| `SeqScan` | `seq_scan.go` | Sequential scan of an in-memory heap table. Applies optional alias. |
| `Filter` | `filter.go` | Evaluates a WHERE predicate and passes matching rows downstream. |
| `Projection` | `projection.go` | Evaluates a list of expressions and outputs new column values. |
| `HashJoin` | `hash_join.go` | Hash join: build a hash table from the right side, probe with the left. Supports INNER and LEFT joins. |
| `NestedLoopJoin` | `nl_join.go` | Nested loop join. Supports INNER, LEFT, RIGHT, and FULL OUTER joins. |
| `SortMergeJoin` | `sort_merge_join.go` | Sort-merge join. Materialises and sorts both sides, then merges. |
| `HashAggregate` | `hash_agg.go` | Hash-based GROUP BY with COUNT, SUM, AVG, MIN, MAX, STDDEV, VAR. |
| `Sort` | `sort.go` | In-memory sort with multi-column ORDER BY and NULL ordering. |
| `Limit` | `limit.go` | Applies LIMIT and OFFSET. |
| `WindowOp` | `window.go` | Window functions: ROW_NUMBER, RANK, DENSE_RANK, NTILE, LAG, LEAD, FIRST_VALUE, LAST_VALUE, NTH_VALUE, and aggregate window functions. |
| `SetOpOp` | `set_op.go` | UNION [ALL], INTERSECT [ALL], EXCEPT [ALL]. |
| `DedupeOp` | `set_op.go` | SELECT DISTINCT — removes duplicate rows using a hash set. |
| `InsertOp` | `insert.go` | INSERT INTO (values or INSERT … SELECT). |
| `UpdateOp` | `update.go` | UPDATE with WHERE clause. |
| `DeleteOp` | `delete.go` | DELETE with WHERE clause. |
| `ExplainOp` | `ddl.go` | EXPLAIN: formats the physical plan as text. |
| `CreateTableOp` | `ddl.go` | CREATE TABLE (with optional CTAS). |
| `DropTableOp` | `ddl.go` | DROP TABLE [IF EXISTS]. |
| `AlterTableOp` | `ddl.go` | ALTER TABLE: ADD/DROP/RENAME COLUMN, RENAME TABLE. |
| `EmptyOp` | `empty.go` | Returns zero rows. Used for WHERE FALSE and empty relations. |
| `ConstantScanOp` | `empty.go` | Returns exactly one empty row. Implicit FROM for `SELECT 1`. |

## Implementing a New Operator

### Step 1 — Create the operator struct

```go
// MyOp is a one-line description. Start with the type name (Go doc convention).
type MyOp struct {
    Child  Operator        // child operator (if any)
    // ... configuration fields from the physical plan node

    ctx    *exectypes.ExecContext
    schema []catalog.Column
}
```

### Step 2 — Implement Open

```go
func (op *MyOp) Open(ctx *exectypes.ExecContext) error {
    op.ctx = ctx
    if err := op.Child.Open(ctx); err != nil {
        return err
    }
    // Initialize state. If you open multiple children and one fails,
    // close the already-opened ones before returning.
    op.schema = buildSchema(op.Child.Schema())
    return nil
}
```

### Step 3 — Implement Next

```go
func (op *MyOp) Next() (*exectypes.Tuple, error) {
    for {
        t, err := op.Child.Next()
        if err != nil {
            return nil, err   // propagate
        }
        if t == nil {
            return nil, nil   // EOF
        }
        // ... transform or filter t
        return transformed, nil
    }
}
```

### Step 4 — Implement Close and Schema

```go
func (op *MyOp) Close() error          { return op.Child.Close() }
func (op *MyOp) Schema() []catalog.Column { return op.schema }
```

### Step 5 — Wire into the planner and executor

- Add a physical plan node in `internal/planner/physical/nodes.go`.
- Convert logical → physical in `internal/planner/physical/builder.go`.
- Add a `case *physical.MyNode:` in `internal/executor/executor.go` `buildOperator`.

### Step 6 — Write tests

Add integration tests in `internal/executor/` using the helper pattern established in the existing `*_test.go` files (run SQL through the full pipeline and assert result rows).

## Expression Evaluation

Use `EvalExpr(expr, tuple, ctx)` from `expression.go` to evaluate any AST expression against a row. It handles literals, column references, binary/unary ops, function calls, CASE, IN, BETWEEN, EXISTS, and scalar subqueries.

```go
v, err := EvalExpr(predicate, tuple, op.ctx)
if err != nil {
    return nil, fmt.Errorf("myop eval: %w", err)
}
if !IsTruthy(v) {
    continue // skip this row
}
```

## Error Handling

- Always wrap errors with context: `fmt.Errorf("myop: %w", err)`.
- Never discard evaluation errors by returning `false` or `NullValue()` silently.
- On error in `Open` after children are opened, close children before returning.
