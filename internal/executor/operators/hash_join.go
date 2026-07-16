package operators

import (
	"errors"
	"fmt"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// errNullKey is returned by joinKey when the join column value is NULL.
// A NULL join key is not an error per se; it just means the row has no match.
var errNullKey = errors.New("null join key")

// HashJoin implements hash join.
// Build phase: consume right (inner) side into a hash map keyed on the join column.
// Probe phase: stream left (outer) side, probe hash map.
type HashJoin struct {
	Left      Operator // probe side
	Right     Operator // build side (inner)
	JoinType  physical.JoinType
	Condition ast.Expression

	ctx         *exectypes.ExecContext
	hashTable   map[string][]exectypes.Tuple
	schema      []catalog.Column
	probeQueue  []exectypes.Tuple // buffered matches for current probe key
	currentLeft *exectypes.Tuple
}

func (op *HashJoin) Schema() []catalog.Column { return op.schema }

func (op *HashJoin) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	ctx.HashJoins++

	if err := op.Left.Open(ctx); err != nil {
		return err
	}
	if err := op.Right.Open(ctx); err != nil {
		op.Left.Close()
		return err
	}

	// Build phase: consume right side
	op.hashTable = make(map[string][]exectypes.Tuple)
	for {
		tuple, err := op.Right.Next()
		if err != nil {
			op.Left.Close()
			return err
		}
		if tuple == nil {
			break
		}
		key, err := op.joinKey(op.Condition, tuple, false)
		if err != nil {
			if !errors.Is(err, errNullKey) {
				return err
			}
			continue // NULL build key: this row cannot match anything, skip
		}
		op.hashTable[key] = append(op.hashTable[key], *tuple)
	}
	op.Right.Close()

	// Build combined schema
	leftSchema := op.Left.Schema()
	rightSchema := op.Right.Schema()
	op.schema = make([]catalog.Column, len(leftSchema)+len(rightSchema))
	copy(op.schema, leftSchema)
	for i, col := range rightSchema {
		op.schema[len(leftSchema)+i] = catalog.Column{
			Name: col.Name, Type: col.Type, Nullable: col.Nullable, Index: len(leftSchema) + i,
		}
	}

	return nil
}

func (op *HashJoin) Next() (*exectypes.Tuple, error) {
	for {
		// Emit buffered matches first
		if len(op.probeQueue) > 0 {
			right := op.probeQueue[0]
			op.probeQueue = op.probeQueue[1:]
			return joinTuples(op.currentLeft, &right, op.schema), nil
		}

		// Fetch next left tuple
		left, err := op.Left.Next()
		if err != nil {
			return nil, err
		}
		if left == nil {
			return nil, nil // EOF
		}
		op.currentLeft = left

		key, err := op.joinKey(op.Condition, left, true)
		if err != nil {
			if !errors.Is(err, errNullKey) {
				// Real evaluation error — propagate it.
				return nil, fmt.Errorf("hash join key eval: %w", err)
			}
			// NULL left key: LEFT JOIN emits null-padded row; INNER JOIN skips.
			if op.JoinType == physical.LeftJoin {
				rightSchema := op.Right.Schema()
				vals := make([]catalog.Value, len(left.Values)+len(rightSchema))
				copy(vals, left.Values)
				for i := range rightSchema {
					vals[len(left.Values)+i] = catalog.NullValue()
				}
				return &exectypes.Tuple{Values: vals, Schema: op.schema}, nil
			}
			continue
		}

		matches := op.hashTable[key]
		if len(matches) == 0 {
			// LEFT JOIN: emit null-padded row
			if op.JoinType == physical.LeftJoin {
				rightSchema := op.Right.Schema()
				vals := make([]catalog.Value, len(left.Values)+len(rightSchema))
				copy(vals, left.Values)
				for i := range rightSchema {
					vals[len(left.Values)+i] = catalog.NullValue()
				}
				return &exectypes.Tuple{Values: vals, Schema: op.schema}, nil
			}
			continue
		}

		op.probeQueue = append(op.probeQueue, matches[1:]...)
		right := matches[0]
		return joinTuples(left, &right, op.schema), nil
	}
}

// joinKey extracts the join key value from a tuple given the join condition.
// isLeft=true → extract from the left (probe) expression; false → from the right (build) expression.
func (op *HashJoin) joinKey(cond ast.Expression, tuple *exectypes.Tuple, isLeft bool) (string, error) {
	if cond == nil {
		return "cross", nil
	}
	// For equality conditions like a.id = b.fk, extract the appropriate side
	if bin, ok := cond.(*ast.BinaryExpr); ok && bin.Op.Type == lexer.EQ {
		var keyExpr ast.Expression
		if isLeft {
			keyExpr = bin.Left
		} else {
			keyExpr = bin.Right
		}
		val, err := EvalExpr(keyExpr, tuple, op.ctx)
		if err != nil {
			// Try the other side if one fails (handles column ordering)
			if isLeft {
				val, err = EvalExpr(bin.Right, tuple, op.ctx)
			} else {
				val, err = EvalExpr(bin.Left, tuple, op.ctx)
			}
			if err != nil {
				return "", err
			}
		}
		if val.IsNull {
			return "", errNullKey
		}
		return val.String(), nil
	}

	// For complex conditions, evaluate the whole condition and group on result
	val, err := EvalExpr(cond, tuple, op.ctx)
	if err != nil {
		return "", err
	}
	return val.String(), nil
}

func (op *HashJoin) Close() error {
	_ = op.Left.Close()
	op.hashTable = nil
	return nil
}

