package operators

import (
	"errors"
	"fmt"
	"sort"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// SortMergeJoin sorts both sides on the join key then merges.
// Supports INNER, LEFT, and RIGHT joins.
type SortMergeJoin struct {
	Left      Operator
	Right     Operator
	JoinType  physical.JoinType
	Condition ast.Expression

	ctx    *exectypes.ExecContext
	schema []catalog.Column

	leftRows  []exectypes.Tuple
	rightRows []exectypes.Tuple

	// Typed key cache: avoids re-evaluation during sort and merge.
	leftKeys  []catalog.Value
	rightKeys []catalog.Value

	// Two-pointer state
	leftIdx  int
	rightIdx int

	// Equal-key group cross-product state
	inGroup         bool
	leftGroupStart  int
	leftGroupEnd    int
	rightGroupStart int
	rightGroupEnd   int
	leftPos         int
	rightPos        int
}

func (op *SortMergeJoin) Schema() []catalog.Column { return op.schema }

func (op *SortMergeJoin) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	ctx.SortOps++

	if err := op.Left.Open(ctx); err != nil {
		return err
	}
	if err := op.Right.Open(ctx); err != nil {
		op.Left.Close()
		return err
	}

	// Materialise both sides
	for {
		t, err := op.Left.Next()
		if err != nil {
			op.Left.Close()
			op.Right.Close()
			return err
		}
		if t == nil {
			break
		}
		op.leftRows = append(op.leftRows, *t)
	}
	op.Left.Close()

	for {
		t, err := op.Right.Next()
		if err != nil {
			op.Right.Close()
			return err
		}
		if t == nil {
			break
		}
		op.rightRows = append(op.rightRows, *t)
	}
	op.Right.Close()

	// Sort both sides using typed Value.Compare — compute keys inline so the
	// comparator always reads the key of the row currently at positions i and j
	// (sort.SliceStable passes current post-swap indices, not original ones).
	sort.SliceStable(op.leftRows, func(i, j int) bool {
		ki, erri := op.extractTypedKey(&op.leftRows[i], true)
		kj, errj := op.extractTypedKey(&op.leftRows[j], true)
		if erri != nil || errj != nil {
			return false
		}
		cmp, err := ki.Compare(kj)
		return err == nil && cmp < 0
	})
	sort.SliceStable(op.rightRows, func(i, j int) bool {
		ki, erri := op.extractTypedKey(&op.rightRows[i], false)
		kj, errj := op.extractTypedKey(&op.rightRows[j], false)
		if erri != nil || errj != nil {
			return false
		}
		cmp, err := ki.Compare(kj)
		return err == nil && cmp < 0
	})

	// Build sorted key arrays for the merge phase.
	op.leftKeys = make([]catalog.Value, len(op.leftRows))
	for i := range op.leftRows {
		k, err := op.extractTypedKey(&op.leftRows[i], true)
		if err != nil {
			op.leftKeys[i] = catalog.NullValue()
		} else {
			op.leftKeys[i] = k
		}
	}
	op.rightKeys = make([]catalog.Value, len(op.rightRows))
	for i := range op.rightRows {
		k, err := op.extractTypedKey(&op.rightRows[i], false)
		if err != nil {
			op.rightKeys[i] = catalog.NullValue()
		} else {
			op.rightKeys[i] = k
		}
	}

	// Build combined schema.
	// LEFT JOIN: right columns nullable. RIGHT JOIN: left columns nullable.
	leftSchema := op.Left.Schema()
	rightSchema := op.Right.Schema()
	op.schema = make([]catalog.Column, len(leftSchema)+len(rightSchema))
	for i, col := range leftSchema {
		nullable := col.Nullable || op.JoinType == physical.RightJoin
		op.schema[i] = catalog.Column{Name: col.Name, Type: col.Type, Nullable: nullable, Index: i}
	}
	for i, col := range rightSchema {
		nullable := col.Nullable || op.JoinType == physical.LeftJoin
		op.schema[len(leftSchema)+i] = catalog.Column{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: nullable,
			Index:    len(leftSchema) + i,
		}
	}

	op.leftIdx = 0
	op.rightIdx = 0
	return nil
}

// extractTypedKey gets a typed catalog.Value join key from a tuple.
func (op *SortMergeJoin) extractTypedKey(tuple *exectypes.Tuple, isLeft bool) (catalog.Value, error) {
	if op.Condition == nil {
		return catalog.IntValue(0), nil // cross join: all keys equal
	}
	bin, ok := op.Condition.(*ast.BinaryExpr)
	if !ok || bin.Op.Type != lexer.EQ {
		val, err := EvalExpr(op.Condition, tuple, op.ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
		return val, nil
	}
	var primary, fallback ast.Expression
	if isLeft {
		primary, fallback = bin.Left, bin.Right
	} else {
		primary, fallback = bin.Right, bin.Left
	}
	val, err := EvalExpr(primary, tuple, op.ctx)
	if err != nil {
		val, err = EvalExpr(fallback, tuple, op.ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
	}
	if val.IsNull {
		return catalog.NullValue(), errNullSMJKey
	}
	return val, nil
}

var errNullSMJKey = errors.New("null sort-merge join key")

func (op *SortMergeJoin) keysEqual(a, b catalog.Value) bool {
	if a.IsNull || b.IsNull {
		return false
	}
	cmp, err := a.Compare(b)
	return err == nil && cmp == 0
}

func (op *SortMergeJoin) keyLess(a, b catalog.Value) bool {
	if a.IsNull {
		return true
	}
	if b.IsNull {
		return false
	}
	cmp, err := a.Compare(b)
	return err == nil && cmp < 0
}

func (op *SortMergeJoin) Next() (*exectypes.Tuple, error) {
	for {
		// Emit cross-product of current equal-key group
		if op.inGroup {
			if op.rightPos < op.rightGroupEnd {
				left := op.leftRows[op.leftPos]
				right := op.rightRows[op.rightPos]
				op.rightPos++
				if op.rightPos == op.rightGroupEnd {
					op.leftPos++
					op.rightPos = op.rightGroupStart
					if op.leftPos == op.leftGroupEnd {
						op.inGroup = false
						op.leftIdx = op.leftGroupEnd
						op.rightIdx = op.rightGroupEnd
					}
				}
				return op.buildOutput(&left, &right), nil
			}
			op.inGroup = false
		}

		// For RIGHT JOIN, swap roles: right is "outer", left is "inner".
		if op.JoinType == physical.RightJoin {
			return op.nextRightJoin()
		}

		// Left exhausted → done
		if op.leftIdx >= len(op.leftRows) {
			return nil, nil
		}

		// Right exhausted
		if op.rightIdx >= len(op.rightRows) {
			if op.JoinType == physical.LeftJoin {
				left := op.leftRows[op.leftIdx]
				op.leftIdx++
				return op.nullPaddedLeft(&left), nil
			}
			return nil, nil
		}

		lKey := op.leftKeys[op.leftIdx]
		rKey := op.rightKeys[op.rightIdx]

		if lKey.IsNull {
			op.leftIdx++
			continue
		}
		if rKey.IsNull {
			op.rightIdx++
			continue
		}

		if op.keyLess(lKey, rKey) {
			if op.JoinType == physical.LeftJoin {
				left := op.leftRows[op.leftIdx]
				op.leftIdx++
				return op.nullPaddedLeft(&left), nil
			}
			op.leftIdx++
		} else if op.keyLess(rKey, lKey) {
			op.rightIdx++
		} else {
			// Equal keys: find extent of both groups
			lEnd := op.leftIdx + 1
			for lEnd < len(op.leftRows) && op.keysEqual(op.leftKeys[lEnd], lKey) {
				lEnd++
			}
			rEnd := op.rightIdx + 1
			for rEnd < len(op.rightRows) && op.keysEqual(op.rightKeys[rEnd], rKey) {
				rEnd++
			}
			op.leftGroupStart = op.leftIdx
			op.leftGroupEnd = lEnd
			op.rightGroupStart = op.rightIdx
			op.rightGroupEnd = rEnd
			op.leftPos = op.leftIdx
			op.rightPos = op.rightIdx
			op.inGroup = true
		}
	}
}

// nextRightJoin implements RIGHT JOIN: right is outer, left is inner.
func (op *SortMergeJoin) nextRightJoin() (*exectypes.Tuple, error) {
	// Right exhausted → done
	if op.rightIdx >= len(op.rightRows) {
		return nil, nil
	}
	// Left exhausted: emit null-padded for remaining right rows
	if op.leftIdx >= len(op.leftRows) {
		right := op.rightRows[op.rightIdx]
		op.rightIdx++
		return op.nullPaddedRight(&right), nil
	}

	lKey := op.leftKeys[op.leftIdx]
	rKey := op.rightKeys[op.rightIdx]

	if lKey.IsNull {
		op.leftIdx++
		return op.nextRightJoin()
	}
	if rKey.IsNull {
		right := op.rightRows[op.rightIdx]
		op.rightIdx++
		return op.nullPaddedRight(&right), nil
	}

	if op.keyLess(lKey, rKey) {
		op.leftIdx++
		return op.nextRightJoin()
	} else if op.keyLess(rKey, lKey) {
		right := op.rightRows[op.rightIdx]
		op.rightIdx++
		return op.nullPaddedRight(&right), nil
	}
	// Equal: find group extents, set inGroup and let Next() handle cross-product
	lEnd := op.leftIdx + 1
	for lEnd < len(op.leftRows) && op.keysEqual(op.leftKeys[lEnd], lKey) {
		lEnd++
	}
	rEnd := op.rightIdx + 1
	for rEnd < len(op.rightRows) && op.keysEqual(op.rightKeys[rEnd], rKey) {
		rEnd++
	}
	op.leftGroupStart = op.leftIdx
	op.leftGroupEnd = lEnd
	op.rightGroupStart = op.rightIdx
	op.rightGroupEnd = rEnd
	op.leftPos = op.leftIdx
	op.rightPos = op.rightIdx
	op.inGroup = true
	return op.Next()
}

func (op *SortMergeJoin) buildOutput(left, right *exectypes.Tuple) *exectypes.Tuple {
	return joinTuples(left, right, op.schema)
}

func (op *SortMergeJoin) nullPaddedLeft(left *exectypes.Tuple) *exectypes.Tuple {
	rightLen := len(op.schema) - len(left.Values)
	vals := make([]catalog.Value, len(op.schema))
	copy(vals, left.Values)
	for i := 0; i < rightLen; i++ {
		vals[len(left.Values)+i] = catalog.NullValue()
	}
	return &exectypes.Tuple{Values: vals, Schema: op.schema}
}

func (op *SortMergeJoin) nullPaddedRight(right *exectypes.Tuple) *exectypes.Tuple {
	leftLen := len(op.schema) - len(right.Values)
	vals := make([]catalog.Value, len(op.schema))
	for i := 0; i < leftLen; i++ {
		vals[i] = catalog.NullValue()
	}
	copy(vals[leftLen:], right.Values)
	return &exectypes.Tuple{Values: vals, Schema: op.schema}
}

func (op *SortMergeJoin) Close() error {
	op.leftRows = nil
	op.rightRows = nil
	op.leftKeys = nil
	op.rightKeys = nil
	return nil
}

// errNullKey is declared in hash_join.go; declare a local alias to avoid import cycles.
var _ = fmt.Sprintf // keep fmt import used
