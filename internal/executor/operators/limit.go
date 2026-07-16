package operators

import (
	"fmt"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// Limit applies LIMIT and OFFSET to its child.
type Limit struct {
	Child  Operator
	Count  ast.Expression
	Offset ast.Expression

	ctx     *exectypes.ExecContext
	count   int64
	offset  int64
	skipped int64
	emitted int64
	schema  []catalog.Column
}

func (op *Limit) Schema() []catalog.Column { return op.schema }

func (op *Limit) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	if err := op.Child.Open(ctx); err != nil {
		return err
	}
	op.schema = op.Child.Schema()

	// Evaluate LIMIT and OFFSET as constants
	if op.Count != nil {
		v, err := EvalExpr(op.Count, nil, nil)
		if err != nil {
			return fmt.Errorf("LIMIT: %w", err)
		}
		if v.Type != catalog.TypeInt {
			return fmt.Errorf("LIMIT must be an integer")
		}
		if v.IntVal < 0 {
			return fmt.Errorf("LIMIT must be non-negative, got %d", v.IntVal)
		}
		op.count = v.IntVal
	} else {
		op.count = -1 // no limit
	}

	if op.Offset != nil {
		v, err := EvalExpr(op.Offset, nil, nil)
		if err != nil {
			return fmt.Errorf("OFFSET: %w", err)
		}
		if v.Type != catalog.TypeInt {
			return fmt.Errorf("OFFSET must be an integer")
		}
		if v.IntVal < 0 {
			return fmt.Errorf("OFFSET must be non-negative, got %d", v.IntVal)
		}
		op.offset = v.IntVal
	}

	op.skipped = 0
	op.emitted = 0
	return nil
}

func (op *Limit) Next() (*exectypes.Tuple, error) {
	if op.count >= 0 && op.emitted >= op.count {
		return nil, nil // limit reached
	}

	for op.skipped < op.offset {
		tuple, err := op.Child.Next()
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			return nil, nil
		}
		op.skipped++
	}

	tuple, err := op.Child.Next()
	if err != nil {
		return nil, err
	}
	if tuple == nil {
		return nil, nil
	}
	op.emitted++
	return tuple, nil
}

func (op *Limit) Close() error {
	return op.Child.Close()
}
