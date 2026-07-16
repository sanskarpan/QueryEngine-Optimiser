# Bug Tracker — Query Engine Optimiser

All 25 defects found during the adversarial audit, ordered by severity.

---

## CRITICAL

### BUG-01 — Data race on global plan ID counters
- **Severity**: Critical
- **Category**: Concurrency / Data Race
- **Location**: `internal/planner/logical/plan.go:22`, `internal/planner/physical/nodes.go:11`
- **Description**: `planIDCounter` and `nodeCounter` are package-level `int` variables incremented without synchronization. Concurrent HTTP requests (handleQuery, handleExplain) both call `ToJSON()`, racing on these counters.
- **Reproduction**: `go test -race ./...` — race detector fires on both counters under concurrent load.
- **Fix**: Replace with `sync/atomic` `int64` atoms (`atomic.AddInt64`).
- **Status**: Fixed

---

### BUG-02 — SortMergeJoin uses string comparison for typed values
- **Severity**: Critical
- **Category**: Correctness
- **Location**: `internal/executor/operators/sort_merge_join.go:82-89`
- **Description**: `extractKey` returns `val.String()` and sort uses `ki < kj` (string comparison). Integers sort lexicographically: "9" > "10", "100" < "99". Join produces wrong matches for integer join keys.
- **Reproduction**: `SELECT c.id, o.id FROM customers c JOIN orders o ON c.id = o.customer_id` via SortMergeJoin returns mismatched rows.
- **Fix**: Use typed `Value.Compare()` in both sort comparators and merge condition; store typed `catalog.Value` as key rather than its string representation.
- **Status**: Fixed

---

### BUG-03 — RIGHT JOIN silently behaves as INNER JOIN
- **Severity**: Critical
- **Category**: Correctness
- **Location**: `internal/executor/operators/nl_join.go:88`, `internal/executor/operators/sort_merge_join.go:174`
- **Description**: Neither `NestedLoopJoin` nor `SortMergeJoin` implements `physical.RightJoin`. The join type check only handles `LeftJoin`; `RightJoin` falls through as INNER JOIN, dropping unmatched right rows.
- **Reproduction**: `SELECT * FROM orders RIGHT JOIN customers ON orders.customer_id = customers.id` returns fewer rows than expected.
- **Fix**: Implement RIGHT JOIN semantics: materialize left side, iterate right rows as outer, emit NULL-padded right rows when no left match found, reorder output columns.
- **Status**: Fixed

---

## HIGH

### BUG-04 — Query timeout context not wired to executor
- **Severity**: High
- **Category**: Reliability
- **Location**: `api/handler.go:127`, `internal/exectypes/types.go`
- **Description**: `handleQuery` creates a `context.WithTimeout` but never passes it to the executor. The timeout is only checked at two static checkpoints; a long-running scan runs indefinitely regardless.
- **Reproduction**: Execute a cartesian join of two large tables; the HTTP deadline fires but execution continues until completion.
- **Fix**: Add `Ctx context.Context` field to `ExecContext`; assign the HTTP context before `executor.Execute()`; check `ctx.Done()` in the main executor row-fetch loop.
- **Status**: Fixed

---

### BUG-05 — `/api/schema/seed` has no authentication
- **Severity**: High
- **Category**: Security
- **Location**: `api/handler.go:364`, `api/server.go:30`
- **Description**: Any unauthenticated client can POST to `/api/schema/seed` and wipe all data. No token or credential check exists.
- **Reproduction**: `curl -X POST http://localhost:8080/api/schema/seed` — data reset without credentials.
- **Fix**: Add optional `seedToken` to `Server`; require `Authorization: Bearer <token>` header when token is non-empty; return 401 otherwise.
- **Status**: Fixed

---

### BUG-06 — Non-atomic catalog+storage creation in handleCreateTable
- **Severity**: High
- **Category**: Correctness / Atomicity
- **Location**: `api/handler.go:348-355`
- **Description**: `handleCreateTable` registers the table in the catalog first, then creates storage. If storage creation fails, the catalog entry persists but has no backing storage, leaving the system in an inconsistent state.
- **Reproduction**: Simulate a storage error after catalog registration — subsequent queries to that table will panic.
- **Fix**: Register in catalog; if storage creation fails, drop the catalog entry before returning the error.
- **Status**: Fixed

---

### BUG-07 — handleCreateTable skips semantic analysis
- **Severity**: High
- **Category**: Correctness
- **Location**: `api/handler.go:306-357`
- **Description**: `handleCreateTable` parses the SQL and builds the catalog entry but never calls `analyzer.Analyze()`. Duplicate column names in `CREATE TABLE` are silently accepted.
- **Reproduction**: `CREATE TABLE t (id INT, id TEXT)` via the API succeeds with two columns named `id`.
- **Fix**: Call `analyzer.New(s.cat).Analyze(stmt)` after parsing, before column construction.
- **Status**: Fixed

---

### BUG-08 — NOT IN with NULL list element returns TRUE (violates SQL three-valued logic)
- **Severity**: High
- **Category**: Correctness / SQL Semantics
- **Location**: `internal/executor/operators/expression.go:424-444`
- **Description**: `evalIn` skips NULL elements in the list. For `NOT IN`, if no match is found and the list contained NULLs, SQL requires returning NULL (not TRUE), because `x <> NULL` is unknown.
- **Reproduction**: `SELECT 5 NOT IN (1, NULL)` returns `true`; SQL standard requires `NULL`.
- **Fix**: Track whether any NULL was seen in the list; if `found=false` and `seenNull=true`, return `NullValue()` instead of `BoolValue(true)`.
- **Status**: Fixed

---

## MEDIUM

### BUG-09 — Register/MustRegister naming convention inverted
- **Severity**: Medium
- **Category**: API / Convention
- **Location**: `internal/catalog/catalog.go:22,33`
- **Description**: Go convention: `Must*` panics. Here `Register` panics and `MustRegister` returns an error — the opposite of the standard convention. Confuses callers and violates the principle of least surprise.
- **Fix**: Rename: the panicking function becomes `MustRegister`; the error-returning function becomes `Register`. Update all callers.
- **Status**: Fixed

---

### BUG-10 — LIKE pattern matching is case-insensitive (non-standard)
- **Severity**: Medium
- **Category**: SQL Semantics
- **Location**: `internal/executor/operators/expression.go:533`
- **Description**: `likeMatch` prepends `(?i)` making LIKE case-insensitive. SQL standard LIKE is case-sensitive. `WHERE name LIKE 'alice%'` should not match `'Alice Smith'`.
- **Fix**: Remove `(?i)` from the pattern builder.
- **Status**: Fixed

---

### BUG-11 — `likeCache` is unbounded
- **Severity**: Medium
- **Category**: Resource Leak
- **Location**: `internal/executor/operators/expression.go:16`
- **Description**: The global `sync.Map` likeCache stores compiled regexes indefinitely. With many distinct LIKE patterns (e.g., user-provided), this grows without bound.
- **Fix**: Add a simple size cap (e.g., 1024 entries); evict oldest entries when full using a secondary `sync.Map` for tracking count, or use an atomic counter gate.
- **Status**: Fixed

---

### BUG-12 — INSERT column name matching is case-sensitive
- **Severity**: Medium
- **Category**: Correctness
- **Location**: `internal/executor/operators/insert.go:68`
- **Description**: `col.Name == name` in `InsertOp.Next()` uses exact-case matching. `INSERT INTO t (ID) VALUES (1)` fails to map `ID` to column `id`.
- **Fix**: Replace with `strings.EqualFold(col.Name, name)`.
- **Status**: Fixed

---

### BUG-13 — UNION/INTERSECT/EXCEPT accepts mismatched column counts
- **Severity**: Medium
- **Category**: Correctness / SQL Semantics
- **Location**: `internal/analyzer/analyzer.go:122`, `internal/executor/operators/set_op.go`
- **Description**: `analyzeSetOp` never validates that left and right sides have the same number of columns. `tupleKey` then encodes unequal-length rows, producing incorrect deduplication.
- **Reproduction**: `SELECT id, name FROM t UNION SELECT id FROM t` — no error reported, wrong results.
- **Fix**: Add column count check in `SetOpOp.Open()` before execution begins.
- **Status**: Fixed

---

### BUG-14 — IN-list selectivity can exceed 1.0
- **Severity**: Medium
- **Category**: Cost Estimation
- **Location**: `internal/optimizer/cost/estimator.go:194`
- **Description**: `selectivity` for `*ast.InExpr` returns `float64(len(p.List)) * 0.1`. With 11+ elements this exceeds 1.0, corrupting cardinality estimates downstream.
- **Fix**: Cap at 1.0: `return math.Min(float64(len(p.List))*0.1, 1.0)`.
- **Status**: Fixed

---

### BUG-15 — NULL join key and evaluation errors conflated in HashJoin
- **Severity**: Medium
- **Category**: Correctness
- **Location**: `internal/executor/operators/hash_join.go:94-106`
- **Description**: `joinKey` returns an error for both NULL values AND expression evaluation failures. In `Next()`, both cases trigger the same LEFT JOIN null-pad branch, masking real errors as missing join keys.
- **Fix**: Distinguish the two cases: return a sentinel `errNullKey` for NULL keys; propagate evaluation errors as actual errors.
- **Status**: Fixed

---

### BUG-16 — `containsAggregate` misses InExpr/BetweenExpr/IsNullExpr subtrees
- **Severity**: Medium
- **Category**: Correctness / Validation
- **Location**: `internal/analyzer/analyzer.go:501-526`
- **Description**: `containsAggregate` does not recurse into `*ast.InExpr`, `*ast.BetweenExpr`, or `*ast.IsNullExpr`. An aggregate inside these expressions in a WHERE clause bypasses the "no aggregates in WHERE" guard.
- **Reproduction**: `SELECT id FROM t WHERE id IN (SELECT COUNT(*) FROM t)` — wait, that's a subquery. More precisely: embedding a direct aggregate call `WHERE id IN (COUNT(x))` would bypass the guard.
- **Fix**: Add cases for `InExpr`, `BetweenExpr`, and `IsNullExpr` in `containsAggregate`, recursing into their sub-expressions.
- **Status**: Fixed

---

### BUG-18 — Negative LIMIT/OFFSET silently becomes unlimited
- **Severity**: Medium
- **Category**: Correctness
- **Location**: `internal/executor/operators/limit.go:43`
- **Description**: `op.count = -1` is the sentinel for "no limit". A negative LIMIT value (e.g., `LIMIT -5`) also produces a negative `count`, which satisfies `count < 0` and silently returns unlimited rows.
- **Fix**: Return an error in `Open()` if LIMIT or OFFSET value is negative.
- **Status**: Fixed

---

### BUG-19 — FROM-less SELECT with WHERE creates nil-child LogicalFilter
- **Severity**: Medium
- **Category**: Crash / Correctness
- **Location**: `internal/planner/logical/builder.go:94`
- **Description**: When `sel.From == nil`, `plan` remains `nil`. If `sel.Where != nil`, `&LogicalFilter{Child: nil}` is created with a nil child, causing a nil-pointer panic in the physical builder.
- **Reproduction**: `SELECT 1 WHERE 1 = 1` — panics.
- **Fix**: When `sel.From == nil`, initialize `plan` with a synthetic constant single-row scan (`&LogicalConstant{}`).
- **Status**: Fixed

---

## LOW

### BUG-17 — MIN/MAX schema type inference returns TypeNull
- **Severity**: Low
- **Category**: Schema / Type Inference
- **Location**: `internal/executor/operators/hash_agg.go:242`, `internal/planner/logical/nodes.go:555`
- **Description**: `aggResultType("MIN")` and `aggResultType("MAX")` return `catalog.TypeNull`. Downstream schema inference for MIN/MAX columns incorrectly reports them as NULL type.
- **Fix**: Return `catalog.TypeText` as a safe fallback for MIN/MAX (actual type depends on input), or inspect the aggregate argument's type from the schema.
- **Status**: Fixed

---

### BUG-20 — Custom `equalFold` is ASCII-only
- **Severity**: Low
- **Category**: Correctness / Unicode
- **Location**: `internal/catalog/table.go:29`
- **Description**: `equalFold` does a byte-level ASCII fold. Non-ASCII column names (e.g. Unicode identifiers) compare incorrectly.
- **Fix**: Replace with `strings.EqualFold`.
- **Status**: Fixed

---

### BUG-21 — `Truncate()` retains backing array (memory not released)
- **Severity**: Low
- **Category**: Resource Leak
- **Location**: `internal/storage/heap.go:40`
- **Description**: `h.rows = h.rows[:0]` sets length to 0 but keeps the backing array allocated. After a large dataset is seeded and truncated, the memory is not released.
- **Fix**: `h.rows = nil` — releases the backing array and allows GC.
- **Status**: Fixed

---

### BUG-22 — SUM/AVG accumulate NaN for TEXT/BOOL values
- **Severity**: Low
- **Category**: Correctness
- **Location**: `internal/executor/operators/hash_agg.go:81`
- **Description**: `toFloat` returns `math.NaN()` for TEXT and BOOL values. Accumulating NaN propagates NaN through the entire sum, silently producing NaN instead of skipping non-numeric values.
- **Fix**: Skip non-numeric values in `accumulate()` for SUM/AVG: only accumulate if `val.Type == TypeInt || val.Type == TypeFloat`.
- **Status**: Fixed

---

### BUG-23 — Missing tests for RIGHT JOIN, NOT IN+NULL, SortMergeJoin correctness
- **Severity**: Low
- **Category**: Test Coverage
- **Location**: `internal/executor/executor_test.go`
- **Description**: No tests cover: RIGHT JOIN behavior, NOT IN with NULL semantics, or SortMergeJoin join key correctness (only row count is asserted).
- **Fix**: Add `TestExecutor_RightJoin`, `TestExecutor_NotInWithNull`, and `TestExecutor_SortMergeJoinCorrectness`.
- **Status**: Fixed

---

### BUG-24 — Dead `peeked*` fields in Lexer struct
- **Severity**: Low
- **Category**: Code Quality
- **Location**: `internal/lexer/lexer.go:15-20`
- **Description**: `peeked bool`, `peekedToken Token`, `savedPos/Line/Col int` are declared but never used. `Peek()` manually saves/restores state on every call without using them.
- **Fix**: Remove the five unused fields.
- **Status**: Fixed

---

### BUG-25 — Integer arithmetic without overflow guards
- **Severity**: Low
- **Category**: Correctness
- **Location**: `internal/catalog/types.go:165,183,196`
- **Description**: `Add`, `Sub`, and `Mul` on `int64` values do not check for overflow. `math.MaxInt64 + 1` silently wraps to `math.MinInt64`.
- **Fix**: Add overflow checks before each int64 arithmetic operation using `math/bits` or manual bounds checks.
- **Status**: Fixed
