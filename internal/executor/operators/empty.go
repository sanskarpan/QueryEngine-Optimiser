package operators

import (
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// EmptyOp produces zero rows (result of WHERE FALSE or EmptyRelation).
type EmptyOp struct {
	Cols []catalog.Column
}

func (op *EmptyOp) Schema() []catalog.Column           { return op.Cols }
func (op *EmptyOp) Open(_ *exectypes.ExecContext) error { return nil }
func (op *EmptyOp) Next() (*exectypes.Tuple, error)     { return nil, nil }
func (op *EmptyOp) Close() error                       { return nil }

// ConstantScanOp produces exactly one empty row (no columns).
// It is the implicit FROM source for SELECT without a FROM clause (e.g. SELECT 1+1).
type ConstantScanOp struct {
	emitted bool
}

func (op *ConstantScanOp) Schema() []catalog.Column           { return nil }
func (op *ConstantScanOp) Open(_ *exectypes.ExecContext) error { return nil }
func (op *ConstantScanOp) Close() error                       { return nil }

func (op *ConstantScanOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true
	return &exectypes.Tuple{Values: nil, Schema: nil}, nil
}
