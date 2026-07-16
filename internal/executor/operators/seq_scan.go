package operators

import (
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/storage"
)

// SeqScan iterates all rows from a heap table.
type SeqScan struct {
	TableName string
	Alias     string
	Table     *catalog.Table

	ctx    *exectypes.ExecContext
	rows   []storage.Tuple
	pos    int
	schema []catalog.Column
}

func (op *SeqScan) Schema() []catalog.Column { return op.schema }

func (op *SeqScan) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx

	heapTable, err := ctx.Storage.GetTable(op.TableName)
	if err != nil {
		return err
	}
	op.rows = heapTable.Scan()
	op.pos = 0

	alias := op.Alias
	if alias == "" {
		alias = op.TableName
	}
	op.schema = make([]catalog.Column, len(op.Table.Columns))
	for i, col := range op.Table.Columns {
		op.schema[i] = catalog.Column{
			Name:     alias + "." + col.Name,
			Type:     col.Type,
			Nullable: col.Nullable,
			PK:       col.PK,
			Index:    i,
		}
	}
	return nil
}

func (op *SeqScan) Next() (*exectypes.Tuple, error) {
	if op.pos >= len(op.rows) {
		return nil, nil // EOF
	}
	row := op.rows[op.pos]
	op.pos++
	op.ctx.RowsScanned++
	return &exectypes.Tuple{Values: row.Values, Schema: op.schema}, nil
}

func (op *SeqScan) Close() error {
	op.rows = nil
	return nil
}
