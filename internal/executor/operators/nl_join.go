package operators

import (
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// NestedLoopJoin implements the nested loop join algorithm.
// Supports INNER, LEFT, RIGHT, and FULL OUTER joins.
type NestedLoopJoin struct {
	Left      Operator
	Right     Operator
	JoinType  physical.JoinType
	Condition ast.Expression

	ctx         *exectypes.ExecContext
	currentLeft *exectypes.Tuple
	schema      []catalog.Column
	rightRows   []exectypes.Tuple // cached right side rows
	leftRows     []exectypes.Tuple // cached left side rows (for RIGHT/FULL JOIN)
	leftClosed   bool             // true once Left.Close() has been called
	rightPos     int
	leftPos      int              // index into leftRows for RIGHT/FULL JOIN inner loop
	leftDone     bool
	// For LEFT JOIN: track if current left tuple had any matches
	leftMatched bool
	emittedNull bool
	// For RIGHT/FULL JOIN: track matched right rows
	rightMatched []bool
	rightPhase   bool // true = emitting unmatched right rows
	// For FULL JOIN: track matched left rows and left-unmatched phase
	leftMatchedFull []bool
	leftPhase       bool // FULL JOIN phase 3: emitting unmatched left rows
	leftPhasePos    int
}

func (op *NestedLoopJoin) Schema() []catalog.Column { return op.schema }

func (op *NestedLoopJoin) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	if err := op.Left.Open(ctx); err != nil {
		return err
	}
	if err := op.Right.Open(ctx); err != nil {
		return err
	}

	// Materialize the right side once
	for {
		tuple, err := op.Right.Next()
		if err != nil {
			return err
		}
		if tuple == nil {
			break
		}
		op.rightRows = append(op.rightRows, *tuple)
	}
	op.Right.Close()

	// For RIGHT JOIN and FULL JOIN, also materialize left side so we can do a two-pass approach.
	if op.JoinType == physical.RightJoin || op.JoinType == physical.FullJoin {
		for {
			tuple, err := op.Left.Next()
			if err != nil {
				return err
			}
			if tuple == nil {
				break
			}
			op.leftRows = append(op.leftRows, *tuple)
		}
		op.Left.Close()
		op.leftClosed = true
		op.rightMatched = make([]bool, len(op.rightRows))
		if op.JoinType == physical.FullJoin {
			op.leftMatchedFull = make([]bool, len(op.leftRows))
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

	op.rightPos = 0
	op.leftPos = 0
	return nil
}

func (op *NestedLoopJoin) Next() (*exectypes.Tuple, error) {
	// RIGHT JOIN and FULL JOIN use a two-pass implementation.
	if op.JoinType == physical.RightJoin || op.JoinType == physical.FullJoin {
		return op.nextRightJoin()
	}

	for {
		// If no current left tuple, fetch one
		if op.currentLeft == nil {
			left, err := op.Left.Next()
			if err != nil {
				return nil, err
			}
			if left == nil {
				return nil, nil // EOF
			}
			op.currentLeft = left
			op.rightPos = 0
			op.leftMatched = false
			op.emittedNull = false
		}

		// LEFT JOIN: if right side is exhausted and no match was found, emit null-padded row
		if op.rightPos >= len(op.rightRows) {
			if op.JoinType == physical.LeftJoin && !op.leftMatched && !op.emittedNull {
				op.emittedNull = true
				saved := op.currentLeft
				op.currentLeft = nil
				return op.buildNullPadded(saved), nil
			}
			op.currentLeft = nil
			continue
		}

		right := op.rightRows[op.rightPos]
		op.rightPos++

		combined := joinTuples(op.currentLeft, &right, op.schema)

		if op.Condition != nil {
			val, err := EvalExpr(op.Condition, combined, op.ctx)
			if err != nil {
				return nil, err
			}
			if !IsTruthy(val) {
				continue
			}
		}

		op.leftMatched = true
		return combined, nil
	}
}

// nextRightJoin implements RIGHT JOIN and FULL JOIN.
// Phase 1: for each right row, iterate all left rows looking for matches.
// Phase 2: emit null-padded rows for unmatched right rows.
// Phase 3 (FULL JOIN only): emit null-padded rows for unmatched left rows.
func (op *NestedLoopJoin) nextRightJoin() (*exectypes.Tuple, error) {
	for {
		// Phase 3 (FULL JOIN only): unmatched left rows
		if op.leftPhase {
			for op.leftPhasePos < len(op.leftRows) {
				if !op.leftMatchedFull[op.leftPhasePos] {
					left := op.leftRows[op.leftPhasePos]
					op.leftPhasePos++
					return op.buildNullPadded(&left), nil
				}
				op.leftPhasePos++
			}
			return nil, nil // EOF
		}

		if op.rightPhase {
			// Phase 2: emit unmatched right rows with NULL-padded left
			for op.rightPos < len(op.rightRows) {
				if !op.rightMatched[op.rightPos] {
					right := op.rightRows[op.rightPos]
					op.rightPos++
					return op.buildNullPaddedRight(&right), nil
				}
				op.rightPos++
			}
			// After phase 2, if FULL JOIN → go to phase 3
			if op.JoinType == physical.FullJoin {
				op.leftPhase = true
				op.leftPhasePos = 0
				continue
			}
			return nil, nil // EOF
		}

		// Phase 1: for the current right row, iterate through left rows
		if op.rightPos >= len(op.rightRows) {
			// Finished phase 1; start phase 2 for unmatched right rows
			op.rightPhase = true
			op.rightPos = 0
			continue
		}

		right := &op.rightRows[op.rightPos]

		if op.leftPos >= len(op.leftRows) {
			// Done with this right row; move to next
			op.rightPos++
			op.leftPos = 0
			continue
		}

		leftIdx := op.leftPos
		left := &op.leftRows[leftIdx]
		op.leftPos++

		combined := joinTuples(left, right, op.schema)

		if op.Condition != nil {
			val, err := EvalExpr(op.Condition, combined, op.ctx)
			if err != nil {
				return nil, err
			}
			if !IsTruthy(val) {
				continue
			}
		}

		op.rightMatched[op.rightPos] = true
		if op.JoinType == physical.FullJoin {
			op.leftMatchedFull[leftIdx] = true
		}
		return combined, nil
	}
}

func (op *NestedLoopJoin) buildNullPadded(left *exectypes.Tuple) *exectypes.Tuple {
	leftSchema := op.Left.Schema()
	rightSchema := op.Right.Schema()
	vals := make([]catalog.Value, len(leftSchema)+len(rightSchema))
	copy(vals, left.Values)
	for i := range rightSchema {
		vals[len(leftSchema)+i] = catalog.NullValue()
	}
	return &exectypes.Tuple{Values: vals, Schema: op.schema}
}

func (op *NestedLoopJoin) buildNullPaddedRight(right *exectypes.Tuple) *exectypes.Tuple {
	leftSchema := op.Left.Schema()
	vals := make([]catalog.Value, len(op.schema))
	for i := range leftSchema {
		vals[i] = catalog.NullValue()
	}
	copy(vals[len(leftSchema):], right.Values)
	return &exectypes.Tuple{Values: vals, Schema: op.schema}
}

func (op *NestedLoopJoin) Close() error {
	if !op.leftClosed {
		_ = op.Left.Close()
	}
	return nil
}

// joinTuples merges two tuples into one with the combined schema.
func joinTuples(left, right *exectypes.Tuple, schema []catalog.Column) *exectypes.Tuple {
	vals := make([]catalog.Value, len(left.Values)+len(right.Values))
	copy(vals, left.Values)
	copy(vals[len(left.Values):], right.Values)
	return &exectypes.Tuple{Values: vals, Schema: schema}
}
