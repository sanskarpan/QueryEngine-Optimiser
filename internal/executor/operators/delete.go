package operators

import (
	"fmt"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/storage"
)

// DeleteOp scans a table, evaluates a WHERE predicate per row, and removes
// matching rows. Returns one tuple: rows_affected.
type DeleteOp struct {
	TableName string
	Table     *catalog.Table
	Where     ast.Expression

	ctx     *exectypes.ExecContext
	schema  []catalog.Column
	emitted bool
}

func (op *DeleteOp) Schema() []catalog.Column { return op.schema }

func (op *DeleteOp) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	op.schema = []catalog.Column{
		{Name: "rows_affected", Type: catalog.TypeInt, Index: 0},
	}
	return nil
}

func (op *DeleteOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true

	ht, err := op.ctx.Storage.GetTable(op.TableName)
	if err != nil {
		return nil, fmt.Errorf("DELETE: table %q not found: %w", op.TableName, err)
	}

	// Build the schema for evaluating WHERE against scanned rows.
	tblSchema := make([]catalog.Column, len(op.Table.Columns))
	for i, col := range op.Table.Columns {
		tblSchema[i] = catalog.Column{
			Name:  op.TableName + "." + col.Name,
			Type:  col.Type,
			Index: i,
		}
	}

	where := op.Where
	ctx := op.ctx

	var whereErr error
	count := ht.DeleteWhere(func(row storage.Tuple) bool {
		if where == nil {
			return true // DELETE without WHERE removes all rows
		}
		tuple := &exectypes.Tuple{Values: row.Values, Schema: tblSchema}
		val, err := EvalExpr(where, tuple, ctx)
		if err != nil {
			whereErr = err
			return false
		}
		return IsTruthy(val)
	})
	if whereErr != nil {
		return nil, fmt.Errorf("DELETE WHERE: %w", whereErr)
	}

	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.IntValue(count)},
		Schema: op.schema,
	}, nil
}

func (op *DeleteOp) Close() error { return nil }
