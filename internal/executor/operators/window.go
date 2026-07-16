package operators

import (
	"fmt"
	"sort"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// WindowOp evaluates window functions over a partitioned, ordered set of rows.
// It materialises all rows from the child, computes window function values for
// each row, and emits rows one at a time with extra columns appended.
type WindowOp struct {
	Child   Operator
	Windows []physical.WindowExpr

	// internal state
	schema []catalog.Column
	rows   []exectypes.Tuple // materialised rows with window cols appended
	pos    int
}

func (op *WindowOp) Schema() []catalog.Column { return op.schema }

func (op *WindowOp) Open(ctx *exectypes.ExecContext) error {
	if err := op.Child.Open(ctx); err != nil {
		return err
	}
	// Materialise child rows
	var childRows []exectypes.Tuple
	for {
		t, err := op.Child.Next()
		if err != nil {
			return fmt.Errorf("window child next: %w", err)
		}
		if t == nil {
			break
		}
		childRows = append(childRows, *t)
	}

	childSchema := op.Child.Schema()
	nWin := len(op.Windows)

	// Build output schema = child schema + one column per window expr
	op.schema = make([]catalog.Column, len(childSchema)+nWin)
	copy(op.schema, childSchema)
	for i, w := range op.Windows {
		name := w.Alias
		if name == "" {
			name = strings.ToLower(w.Expr.Func.Name)
		}
		dt := winColType(w.Expr.Func.Name)
		op.schema[len(childSchema)+i] = catalog.Column{Name: name, Type: dt, Index: len(childSchema) + i}
	}

	// Allocate output rows (child values + window slots)
	op.rows = make([]exectypes.Tuple, len(childRows))
	for i, cr := range childRows {
		vals := make([]catalog.Value, len(cr.Values)+nWin)
		copy(vals, cr.Values)
		op.rows[i] = exectypes.Tuple{Values: vals, Schema: op.schema}
	}

	// Compute each window function
	for winIdx, w := range op.Windows {
		if err := op.computeWindow(ctx, winIdx, len(childSchema), w, childRows); err != nil {
			return err
		}
	}

	op.pos = 0
	return nil
}

func (op *WindowOp) Next() (*exectypes.Tuple, error) {
	if op.pos >= len(op.rows) {
		return nil, nil
	}
	t := op.rows[op.pos]
	op.pos++
	return &t, nil
}

func (op *WindowOp) Close() error { return op.Child.Close() }

// -----------------------------------------------------------------------
// Window computation
// -----------------------------------------------------------------------

func (op *WindowOp) computeWindow(
	ctx *exectypes.ExecContext,
	winIdx, baseOff int,
	w physical.WindowExpr,
	childRows []exectypes.Tuple,
) error {
	spec := w.Expr.Over
	fn := strings.ToUpper(w.Expr.Func.Name)

	// Partition the row indices
	partitions := partitionRows(childRows, spec, ctx)

	for _, part := range partitions {
		// Sort within partition by ORDER BY spec
		sortedIdxs := make([]int, len(part))
		copy(sortedIdxs, part)
		if spec != nil && len(spec.OrderBy) > 0 {
			sortedIdxs = sortPartition(sortedIdxs, childRows, spec.OrderBy, ctx)
		}

		// Compute function over sorted partition
		if err := op.applyWindowFunc(ctx, fn, winIdx, baseOff, w, childRows, sortedIdxs); err != nil {
			return err
		}
	}
	return nil
}

// applyWindowFunc dispatches to the appropriate window function computation.
func (op *WindowOp) applyWindowFunc(
	ctx *exectypes.ExecContext,
	fn string,
	winIdx, baseOff int,
	w physical.WindowExpr,
	childRows []exectypes.Tuple,
	sortedIdxs []int,
) error {
	fc := w.Expr.Func
	colIdx := baseOff + winIdx

	switch fn {
	case "ROW_NUMBER":
		for rank, rowIdx := range sortedIdxs {
			op.rows[rowIdx].Values[colIdx] = catalog.IntValue(int64(rank + 1))
		}

	case "RANK":
		// Rows with equal ORDER BY values share the same rank, with gaps
		ranks := computeRank(childRows, sortedIdxs, w.Expr.Over, ctx, false)
		for i, rowIdx := range sortedIdxs {
			op.rows[rowIdx].Values[colIdx] = catalog.IntValue(ranks[i])
		}

	case "DENSE_RANK":
		ranks := computeRank(childRows, sortedIdxs, w.Expr.Over, ctx, true)
		for i, rowIdx := range sortedIdxs {
			op.rows[rowIdx].Values[colIdx] = catalog.IntValue(ranks[i])
		}

	case "NTILE":
		n := int64(1)
		if len(fc.Args) > 0 {
			v, err := EvalExpr(fc.Args[0], nil, ctx)
			if err == nil && !v.IsNull && v.Type == catalog.TypeInt {
				n = v.IntVal
			}
		}
		if n < 1 {
			n = 1
		}
		total := int64(len(sortedIdxs))
		for i, rowIdx := range sortedIdxs {
			bucket := (int64(i)*n)/total + 1
			op.rows[rowIdx].Values[colIdx] = catalog.IntValue(bucket)
		}

	case "LAG", "LEAD":
		offset := int64(1)
		var defaultVal catalog.Value = catalog.NullValue()
		if len(fc.Args) > 1 {
			v, err := EvalExpr(fc.Args[1], nil, ctx)
			if err == nil && !v.IsNull {
				offset = v.IntVal
			}
		}
		if len(fc.Args) > 2 {
			v, err := EvalExpr(fc.Args[2], nil, ctx)
			if err == nil {
				defaultVal = v
			}
		}
		for i, rowIdx := range sortedIdxs {
			peer := -1
			if fn == "LAG" {
				peer = i - int(offset)
			} else {
				peer = i + int(offset)
			}
			if peer < 0 || peer >= len(sortedIdxs) {
				op.rows[rowIdx].Values[colIdx] = defaultVal
			} else {
				t := &childRows[sortedIdxs[peer]]
				v, err := EvalExpr(fc.Args[0], t, ctx)
				if err != nil {
					return err
				}
				op.rows[rowIdx].Values[colIdx] = v
			}
		}

	case "FIRST_VALUE":
		if len(sortedIdxs) == 0 || len(fc.Args) == 0 {
			break
		}
		winSpec := w.Expr.Over
		for i, rowIdx := range sortedIdxs {
			frame := resolveFrame(i, len(sortedIdxs), winSpec, childRows, sortedIdxs, ctx)
			firstT := &childRows[sortedIdxs[frame.start]]
			v, err := EvalExpr(fc.Args[0], firstT, ctx)
			if err != nil {
				return err
			}
			op.rows[rowIdx].Values[colIdx] = v
		}

	case "LAST_VALUE":
		if len(sortedIdxs) == 0 || len(fc.Args) == 0 {
			break
		}
		winSpec := w.Expr.Over
		for i, rowIdx := range sortedIdxs {
			frame := resolveFrame(i, len(sortedIdxs), winSpec, childRows, sortedIdxs, ctx)
			lastT := &childRows[sortedIdxs[frame.end]]
			v, err := EvalExpr(fc.Args[0], lastT, ctx)
			if err != nil {
				return err
			}
			op.rows[rowIdx].Values[colIdx] = v
		}

	case "NTH_VALUE":
		if len(sortedIdxs) == 0 || len(fc.Args) < 2 {
			break
		}
		winSpec := w.Expr.Over
		nv, _ := EvalExpr(fc.Args[1], nil, ctx)
		nthOffset := int(nv.IntVal) - 1 // 1-based to 0-based
		for i, rowIdx := range sortedIdxs {
			frame := resolveFrame(i, len(sortedIdxs), winSpec, childRows, sortedIdxs, ctx)
			frameSlice := sortedIdxs[frame.start : frame.end+1]
			if nthOffset >= 0 && nthOffset < len(frameSlice) {
				t := &childRows[frameSlice[nthOffset]]
				v, err := EvalExpr(fc.Args[0], t, ctx)
				if err != nil {
					return err
				}
				op.rows[rowIdx].Values[colIdx] = v
			} else {
				op.rows[rowIdx].Values[colIdx] = catalog.NullValue()
			}
		}

	// Aggregate window functions: SUM, COUNT, AVG, MIN, MAX over frame
	case "SUM", "COUNT", "AVG", "MIN", "MAX":
		spec := w.Expr.Over
		for i, rowIdx := range sortedIdxs {
			frame := resolveFrame(i, len(sortedIdxs), spec, childRows, sortedIdxs, ctx)
			v, err := evalWindowAgg(fn, fc, childRows, sortedIdxs[frame.start:frame.end+1], ctx)
			if err != nil {
				return err
			}
			op.rows[rowIdx].Values[colIdx] = v
		}

	default:
		// Unknown window function — fill with NULL
		for _, rowIdx := range sortedIdxs {
			op.rows[rowIdx].Values[colIdx] = catalog.NullValue()
		}
	}
	return nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

type frameRange struct {
	start, end int
}

// resolveFrame computes the frame bounds for row i in a partition of size n.
// For RANGE mode with CURRENT ROW bounds, it expands to include all peer rows
// (rows with the same ORDER BY key as row i).
func resolveFrame(i, n int, spec *ast.WindowSpec, childRows []exectypes.Tuple, sortedIdxs []int, ctx *exectypes.ExecContext) frameRange {
	if spec == nil || spec.Frame == nil {
		// Default: RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
		return frameRange{start: 0, end: i}
	}
	f := spec.Frame
	start := resolveBound(f.Start, i, n)
	end := resolveBound(f.End, i, n)

	// RANGE mode: CURRENT ROW means "all peers with the same ORDER BY key value"
	if f.Mode == "RANGE" && len(spec.OrderBy) > 0 {
		currentKey := orderByKey(i, childRows, sortedIdxs, spec.OrderBy, ctx)
		if f.End.Kind == ast.FrameCurrentRow {
			j := i
			for j+1 < n && orderByKey(j+1, childRows, sortedIdxs, spec.OrderBy, ctx) == currentKey {
				j++
			}
			end = j
		}
		if f.Start.Kind == ast.FrameCurrentRow {
			j := i
			for j > 0 && orderByKey(j-1, childRows, sortedIdxs, spec.OrderBy, ctx) == currentKey {
				j--
			}
			start = j
		}
	}

	if start < 0 {
		start = 0
	}
	if end >= n {
		end = n - 1
	}
	return frameRange{start: start, end: end}
}

// orderByKey returns a string key representing the ORDER BY values for position i.
func orderByKey(i int, childRows []exectypes.Tuple, sortedIdxs []int, orderBy []*ast.SortSpec, ctx *exectypes.ExecContext) string {
	row := &childRows[sortedIdxs[i]]
	var parts []string
	for _, s := range orderBy {
		v, _ := EvalExpr(s.Expr, row, ctx)
		parts = append(parts, v.String())
	}
	return strings.Join(parts, "\x00")
}

func resolveBound(b ast.FrameBound, i, n int) int {
	switch b.Kind {
	case ast.FrameUnboundedPreceding:
		return 0
	case ast.FrameCurrentRow:
		return i
	case ast.FrameUnboundedFollowing:
		return n - 1
	case ast.FrameNPreceding:
		if b.Offset != nil {
			if lit, ok := b.Offset.(*ast.IntLiteral); ok {
				return i - int(lit.Value)
			}
		}
		return i - 1
	case ast.FrameNFollowing:
		if b.Offset != nil {
			if lit, ok := b.Offset.(*ast.IntLiteral); ok {
				return i + int(lit.Value)
			}
		}
		return i + 1
	default:
		return i
	}
}

// partitionRows groups row indices by PARTITION BY key.
func partitionRows(rows []exectypes.Tuple, spec *ast.WindowSpec, ctx *exectypes.ExecContext) [][]int {
	if spec == nil || len(spec.PartitionBy) == 0 {
		// Single global partition
		idxs := make([]int, len(rows))
		for i := range rows {
			idxs[i] = i
		}
		return [][]int{idxs}
	}

	keyOf := func(row *exectypes.Tuple) string {
		var parts []string
		for _, expr := range spec.PartitionBy {
			v, _ := EvalExpr(expr, row, ctx)
			parts = append(parts, v.String())
		}
		return strings.Join(parts, "\x00")
	}

	partMap := make(map[string][]int)
	var order []string
	for i := range rows {
		k := keyOf(&rows[i])
		if _, seen := partMap[k]; !seen {
			order = append(order, k)
		}
		partMap[k] = append(partMap[k], i)
	}
	result := make([][]int, len(order))
	for i, k := range order {
		result[i] = partMap[k]
	}
	return result
}

// sortPartition returns a sorted copy of idx based on ORDER BY specs.
func sortPartition(idxs []int, rows []exectypes.Tuple, specs []*ast.SortSpec, ctx *exectypes.ExecContext) []int {
	sorted := make([]int, len(idxs))
	copy(sorted, idxs)
	sort.SliceStable(sorted, func(a, b int) bool {
		ra, rb := &rows[sorted[a]], &rows[sorted[b]]
		for _, spec := range specs {
			va, _ := EvalExpr(spec.Expr, ra, ctx)
			vb, _ := EvalExpr(spec.Expr, rb, ctx)
			cmp, err := va.Compare(vb)
			if err != nil || cmp == 0 {
				continue
			}
			if spec.Ascending {
				return cmp < 0
			}
			return cmp > 0
		}
		return false
	})
	return sorted
}

// computeRank computes RANK or DENSE_RANK for a sorted partition.
func computeRank(
	rows []exectypes.Tuple,
	sortedIdxs []int,
	spec *ast.WindowSpec,
	ctx *exectypes.ExecContext,
	dense bool,
) []int64 {
	n := len(sortedIdxs)
	ranks := make([]int64, n)
	if n == 0 {
		return ranks
	}

	orderExprs := []*ast.SortSpec{}
	if spec != nil {
		orderExprs = spec.OrderBy
	}

	peerKey := func(i int) string {
		row := &rows[sortedIdxs[i]]
		var parts []string
		for _, s := range orderExprs {
			v, _ := EvalExpr(s.Expr, row, ctx)
			parts = append(parts, v.String())
		}
		return strings.Join(parts, "\x00")
	}

	var rank, counter int64 = 1, 0
	prevKey := peerKey(0)
	for i := range sortedIdxs {
		counter++
		k := peerKey(i)
		if k != prevKey {
			if dense {
				rank++
			} else {
				rank = counter
			}
			prevKey = k
		}
		ranks[i] = rank
	}
	return ranks
}

// evalWindowAgg evaluates an aggregate window function over a slice of rows.
func evalWindowAgg(
	fn string,
	fc *ast.FunctionCall,
	allRows []exectypes.Tuple,
	frameIdxs []int,
	ctx *exectypes.ExecContext,
) (catalog.Value, error) {
	switch fn {
	case "COUNT":
		if fc.StarArg {
			return catalog.IntValue(int64(len(frameIdxs))), nil
		}
		count := int64(0)
		for _, idx := range frameIdxs {
			v, _ := EvalExpr(fc.Args[0], &allRows[idx], ctx)
			if !v.IsNull {
				count++
			}
		}
		return catalog.IntValue(count), nil
	case "SUM":
		var sum float64
		hasNonNull := false
		for _, idx := range frameIdxs {
			v, _ := EvalExpr(fc.Args[0], &allRows[idx], ctx)
			if v.IsNull {
				continue
			}
			hasNonNull = true
			sum += toFloat(v)
		}
		if !hasNonNull {
			return catalog.NullValue(), nil
		}
		return catalog.FloatValue(sum), nil
	case "AVG":
		var sum float64
		count := 0
		for _, idx := range frameIdxs {
			v, _ := EvalExpr(fc.Args[0], &allRows[idx], ctx)
			if v.IsNull {
				continue
			}
			sum += toFloat(v)
			count++
		}
		if count == 0 {
			return catalog.NullValue(), nil
		}
		return catalog.FloatValue(sum / float64(count)), nil
	case "MIN":
		var minV catalog.Value
		for _, idx := range frameIdxs {
			v, _ := EvalExpr(fc.Args[0], &allRows[idx], ctx)
			if v.IsNull {
				continue
			}
			if minV.IsNull {
				minV = v
				continue
			}
			if cmp, _ := v.Compare(minV); cmp < 0 {
				minV = v
			}
		}
		return minV, nil
	case "MAX":
		var maxV catalog.Value
		for _, idx := range frameIdxs {
			v, _ := EvalExpr(fc.Args[0], &allRows[idx], ctx)
			if v.IsNull {
				continue
			}
			if maxV.IsNull {
				maxV = v
				continue
			}
			if cmp, _ := v.Compare(maxV); cmp > 0 {
				maxV = v
			}
		}
		return maxV, nil
	}
	return catalog.NullValue(), nil
}

func winColType(fn string) catalog.DataType {
	switch strings.ToUpper(fn) {
	case "ROW_NUMBER", "RANK", "DENSE_RANK", "NTILE":
		return catalog.TypeInt
	case "SUM", "AVG":
		return catalog.TypeFloat
	default:
		return catalog.TypeNull
	}
}
