# Contributing

## Project Structure

```
QueryEngine-Optimiser/
├── api/                        # HTTP API (handlers, middleware, DTOs)
├── cmd/server/                 # Main entry point
├── internal/
│   ├── lexer/                  # SQL tokenizer
│   ├── ast/                    # Abstract syntax tree node types
│   ├── parser/                 # Recursive-descent parser
│   ├── analyzer/               # Semantic analysis and name resolution
│   ├── planner/
│   │   ├── logical/            # Logical plan builder and node types
│   │   └── physical/           # Physical plan builder and node types
│   ├── optimizer/
│   │   └── rule/               # Optimization rules (folding, pushdown)
│   ├── executor/
│   │   └── operators/          # Volcano iterator operators
│   ├── catalog/                # Schema: tables, columns, data types, values
│   ├── storage/                # In-memory heap table storage
│   ├── exectypes/              # Shared execution types (avoids circular imports)
│   └── stats/                  # Table statistics for cost-based optimization
└── web/                        # React frontend
```

The SQL execution pipeline is:

```
SQL string
  → Lexer (tokens)
  → Parser (AST)
  → Analyzer (name resolution, type checks)
  → Logical Planner (LogicalPlan tree)
  → Optimizer (rule-based + CBO rewrites)
  → Physical Planner (PhysicalPlan tree)
  → Executor (Volcano iterator, row-by-row)
  → Result
```

## How to Add a New SQL Operator

Every operator implements the `operators.Operator` interface:

```go
type Operator interface {
    Open(ctx *exectypes.ExecContext) error
    Next() (*exectypes.Tuple, error)
    Close() error
    Schema() []catalog.Column
}
```

**Lifecycle contract:**
- `Open` is called once; initialize state, open child operators, materialize if needed.
- `Next` is called repeatedly until it returns `(nil, nil)` (EOF) or `(nil, err)`.
- `Close` is called exactly once after the last `Next`; release resources, close children.

**Step-by-step:**

1. Create `internal/executor/operators/my_op.go`:

```go
// MyOp is a one-line description of what this operator does.
type MyOp struct {
    Child Operator
    // ... fields
    ctx    *exectypes.ExecContext
    schema []catalog.Column
}

func (op *MyOp) Schema() []catalog.Column { return op.schema }

func (op *MyOp) Open(ctx *exectypes.ExecContext) error {
    op.ctx = ctx
    if err := op.Child.Open(ctx); err != nil {
        return err
    }
    op.schema = op.Child.Schema()
    return nil
}

func (op *MyOp) Next() (*exectypes.Tuple, error) {
    return op.Child.Next() // adapt as needed
}

func (op *MyOp) Close() error { return op.Child.Close() }
```

2. Add a physical plan node in `internal/planner/physical/nodes.go`.

3. Wire it in `internal/planner/physical/builder.go` (`Build` switch).

4. Wire it in `internal/executor/executor.go` (`buildOperator` switch).

5. If it needs a logical plan node, add it in `internal/planner/logical/nodes.go` and wire it in `internal/planner/logical/builder.go`.

6. Write tests in `internal/executor/` (see existing `*_test.go` files for patterns).

**Resource-leak rule:** If `Open` returns an error after opening child operators, close them before returning. If `executor.Execute` calls `Open` and it fails, `Close` is not called automatically.

## How to Add a New Optimizer Rule

1. Create `internal/optimizer/rule/my_rule.go`:

```go
// MyRule is a one-line description of what this rule does.
type MyRule struct{}

func (r MyRule) Name() string { return "MyRule" }

func (r MyRule) Apply(plan logical.Plan) (logical.Plan, bool) {
    // Return (transformedPlan, true) if a change was made.
    // Return (plan, false) if nothing changed.
}
```

2. Register it in `internal/optimizer/optimizer.go` inside `New()`:

```go
func New() *Optimizer {
    return NewWithRules([]rule.Rule{
        rule.ConstantFolding{},
        rule.PredicatePushdown{},
        rule.ProjectionPushdown{},
        rule.MyRule{},  // add here
    })
}
```

3. Write tests in `internal/optimizer/` following the existing test patterns.

## How to Run Tests

```bash
# Run all tests with verbose output
make test

# Run a specific package
go test ./internal/executor/... -v -count=1

# Run a specific test
go test ./internal/executor/... -run TestSQL_Window -v
```

## Coding Conventions

- **Errors**: Wrap errors with context using `fmt.Errorf("operation: %w", err)`. Never discard errors silently.
- **Doc comments**: Every exported type and function must have a Go doc comment starting with the identifier name.
- **No panics**: Use error returns instead of `panic`. Panics are only acceptable for truly impossible invariant violations.
- **Imports**: Use the project module path `github.com/query-engine/query-engine/...` for internal packages.

## PR Guidelines

- One logical change per PR; reference the relevant GitHub issue.
- All tests must pass (`make test`).
- No new `go vet` warnings (`make lint`).
