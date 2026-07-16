package operators

import (
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// Filter evaluates a predicate over its child and yields matching tuples.
type Filter struct {
	ctx       *exectypes.ExecContext
	Child     Operator
	Predicate ast.Expression
}

func (op *Filter) Schema() []catalog.Column { return op.Child.Schema() }

func (op *Filter) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	return op.Child.Open(ctx)
}

func (op *Filter) Next() (*exectypes.Tuple, error) {
	for {
		tuple, err := op.Child.Next()
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			return nil, nil // EOF
		}
		val, err := EvalExpr(op.Predicate, tuple, op.ctx)
		if err != nil {
			return nil, err
		}
		if IsTruthy(val) {
			return tuple, nil
		}
		// Skip non-matching tuple
	}
}

func (op *Filter) Close() error {
	return op.Child.Close()
}
