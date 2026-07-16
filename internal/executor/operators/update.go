package operators

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/storage"
)

// UpdateAssign pairs a column name with the new value expression.
type UpdateAssign struct {
	Column string
	Value  ast.Expression
}

// UpdateOp scans a table, evaluates a WHERE predicate per row, and applies
// column assignments to each matching row. Returns one tuple: rows_affected.
type UpdateOp struct {
	TableName string
	Table     *catalog.Table
	Assigns   []UpdateAssign
	Where     ast.Expression

	ctx     *exectypes.ExecContext
	schema  []catalog.Column
	emitted bool
}

func (op *UpdateOp) Schema() []catalog.Column { return op.schema }

func (op *UpdateOp) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	op.schema = []catalog.Column{
		{Name: "rows_affected", Type: catalog.TypeInt, Index: 0},
	}
	return nil
}

func (op *UpdateOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true

	ht, err := op.ctx.Storage.GetTable(op.TableName)
	if err != nil {
		return nil, fmt.Errorf("UPDATE: table %q not found: %w", op.TableName, err)
	}

	// Build the schema for evaluating expressions against scanned rows.
	tblSchema := make([]catalog.Column, len(op.Table.Columns))
	for i, col := range op.Table.Columns {
		tblSchema[i] = catalog.Column{
			Name:  op.TableName + "." + col.Name,
			Type:  col.Type,
			Index: i,
		}
	}

	where := op.Where
	assigns := op.Assigns
	ctx := op.ctx

	var whereErr, updateErr error
	count := ht.UpdateWhere(
		func(row storage.Tuple) bool {
			if where == nil {
				return true
			}
			tuple := &exectypes.Tuple{Values: row.Values, Schema: tblSchema}
			val, err := EvalExpr(where, tuple, ctx)
			if err != nil {
				whereErr = err
				return false
			}
			return IsTruthy(val)
		},
		func(row storage.Tuple) storage.Tuple {
			newVals := make([]catalog.Value, len(row.Values))
			copy(newVals, row.Values)
			tuple := &exectypes.Tuple{Values: row.Values, Schema: tblSchema}
			for _, a := range assigns {
				idx := findColIndex(op.Table, a.Column)
				if idx < 0 {
					continue
				}
				val, err := EvalExpr(a.Value, tuple, ctx)
				if err != nil {
					updateErr = err
					return row // leave unchanged
				}
				newVals[idx] = val
			}
			return storage.Tuple{Values: newVals}
		},
	)
	if whereErr != nil {
		return nil, whereErr
	}
	if updateErr != nil {
		return nil, updateErr
	}

	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.IntValue(count)},
		Schema: op.schema,
	}, nil
}

func (op *UpdateOp) Close() error { return nil }

// findColIndex finds the index of a column by name (case-insensitive).
func findColIndex(table *catalog.Table, name string) int {
	for i, col := range table.Columns {
		if strings.EqualFold(col.Name, name) {
			return i
		}
	}
	return -1
}
