package operators

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/storage"
)

// InsertOp inserts one or more rows into a storage table and returns a single-row result
// containing the number of rows affected.
type InsertOp struct {
	TableName string
	Table     *catalog.Table
	Columns   []string          // column names in value order; empty = all columns in order
	ValueRows [][]ast.Expression
	SelectSrc Operator // non-nil for INSERT ... SELECT

	ctx     *exectypes.ExecContext
	schema  []catalog.Column
	emitted bool
}

func (op *InsertOp) Schema() []catalog.Column { return op.schema }

func (op *InsertOp) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	op.schema = []catalog.Column{
		{Name: "rows_affected", Type: catalog.TypeInt, Index: 0},
	}
	if op.SelectSrc != nil {
		return op.SelectSrc.Open(ctx)
	}
	return nil
}

func (op *InsertOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true

	ht, err := op.ctx.Storage.GetTable(op.TableName)
	if err != nil {
		return nil, fmt.Errorf("INSERT: table %q not found: %w", op.TableName, err)
	}

	tbl := op.Table
	var rowsAffected int64

	if op.SelectSrc != nil {
		// INSERT ... SELECT: drain the child operator
		for {
			t, err := op.SelectSrc.Next()
			if err != nil {
				return nil, fmt.Errorf("INSERT SELECT: %w", err)
			}
			if t == nil {
				break
			}
			row := op.buildRow(tbl, t.Values, op.SelectSrc.Schema())
			ht.Insert(storage.Tuple{Values: row})
			rowsAffected++
		}
	} else {
		for _, rowExprs := range op.ValueRows {
			vals := make([]catalog.Value, len(rowExprs))
			for i, expr := range rowExprs {
				v, err := EvalExpr(expr, nil, op.ctx)
				if err != nil {
					return nil, fmt.Errorf("INSERT value %d: %w", i+1, err)
				}
				vals[i] = v
			}
			row := op.buildRowFromValues(tbl, vals)
			ht.Insert(storage.Tuple{Values: row})
			rowsAffected++
		}
	}

	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.IntValue(rowsAffected)},
		Schema: op.schema,
	}, nil
}

// buildRowFromValues maps positional or named values to table column positions.
func (op *InsertOp) buildRowFromValues(tbl *catalog.Table, vals []catalog.Value) []catalog.Value {
	row := make([]catalog.Value, len(tbl.Columns))
	for i := range row {
		row[i] = catalog.NullValue()
	}
	if len(op.Columns) == 0 {
		for i := range tbl.Columns {
			if i < len(vals) {
				row[i] = vals[i]
			}
		}
	} else {
		for i, name := range op.Columns {
			for j, col := range tbl.Columns {
				if strings.EqualFold(col.Name, name) {
					if i < len(vals) {
						row[j] = vals[i]
					}
					break
				}
			}
		}
	}
	return row
}

// buildRow maps SELECT output columns to table column positions for INSERT SELECT.
func (op *InsertOp) buildRow(tbl *catalog.Table, vals []catalog.Value, srcSchema []catalog.Column) []catalog.Value {
	row := make([]catalog.Value, len(tbl.Columns))
	for i := range row {
		row[i] = catalog.NullValue()
	}
	if len(op.Columns) == 0 {
		// Positional mapping
		for i := range tbl.Columns {
			if i < len(vals) {
				row[i] = vals[i]
			}
		}
	} else {
		for i, name := range op.Columns {
			for j, col := range tbl.Columns {
				if strings.EqualFold(col.Name, name) {
					if i < len(vals) {
						row[j] = vals[i]
					}
					break
				}
			}
		}
	}
	return row
}

func (op *InsertOp) Close() error {
	if op.SelectSrc != nil {
		return op.SelectSrc.Close()
	}
	return nil
}
