package operators

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
)

// ExplainOp formats the inner physical plan as human-readable text rows.
// When Analyze is true it also executes the plan and appends actual row counts.
type ExplainOp struct {
	Inner   Operator
	Analyze bool
	Plan    physical.Plan // keep the physical plan for printing

	schema  []catalog.Column
	rows    []string
	pos     int
	ctx     *exectypes.ExecContext
}

func (op *ExplainOp) Schema() []catalog.Column { return op.schema }

func (op *ExplainOp) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	op.schema = []catalog.Column{{Name: "Plan", Type: catalog.TypeText, Index: 0}}
	op.pos = 0

	// Format the plan tree
	planStr := physical.PrintPlan(op.Plan)
	op.rows = strings.Split(planStr, "\n")

	if op.Analyze {
		// Run the inner plan to collect actual stats
		if err := op.Inner.Open(ctx); err != nil {
			return err
		}
		rowCount := 0
		for {
			t, err := op.Inner.Next()
			if err != nil {
				return err
			}
			if t == nil {
				break
			}
			rowCount++
		}
		op.Inner.Close()
		op.rows = append(op.rows, fmt.Sprintf("Actual rows produced: %d", rowCount))
		op.rows = append(op.rows, fmt.Sprintf("Rows scanned: %d", ctx.RowsScanned))
	}
	return nil
}

func (op *ExplainOp) Next() (*exectypes.Tuple, error) {
	if op.pos >= len(op.rows) {
		return nil, nil
	}
	line := op.rows[op.pos]
	op.pos++
	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.TextValue(line)},
		Schema: op.schema,
	}, nil
}

func (op *ExplainOp) Close() error { return nil }

// CreateTableOp creates a table in the catalog and storage.
// If SelectSrc is non-nil, it also populates the new table via CREATE TABLE … AS SELECT.
type CreateTableOp struct {
	TableName string
	Columns   []*ast.ColumnDef
	SelectSrc Operator // non-nil for CREATE TABLE ... AS SELECT
	schema    []catalog.Column
	emitted   bool
}

func (op *CreateTableOp) Schema() []catalog.Column { return op.schema }

func (op *CreateTableOp) Open(ctx *exectypes.ExecContext) error {
	op.schema = []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}

	// Parse column definitions and register in catalog.
	var cols []catalog.Column
	for i, cd := range op.Columns {
		dt, err := catalog.ParseDataType(cd.TypeName)
		if err != nil {
			return fmt.Errorf("CREATE TABLE %s: column %s: %w", op.TableName, cd.Name, err)
		}
		cols = append(cols, catalog.Column{
			Name:     cd.Name,
			Type:     dt,
			Nullable: !cd.NotNull,
			PK:       cd.PrimaryKey,
			Index:    i,
		})
	}

	// For CREATE TABLE ... AS SELECT, derive columns from the SELECT output.
	if op.SelectSrc != nil {
		if err := op.SelectSrc.Open(ctx); err != nil {
			return err
		}
		// Use SELECT schema to define table columns.
		for i, sc := range op.SelectSrc.Schema() {
			name := sc.Name
			// Strip any "alias." prefix
			if idx := strings.LastIndex(name, "."); idx >= 0 {
				name = name[idx+1:]
			}
			cols = append(cols, catalog.Column{Name: name, Type: sc.Type, Nullable: true, Index: i})
		}
	}

	tbl := &catalog.Table{Name: op.TableName, Columns: cols}
	if err := ctx.Catalog.Register(tbl); err != nil {
		return fmt.Errorf("CREATE TABLE %s: %w", op.TableName, err)
	}
	if err := ctx.Storage.CreateTable(op.TableName); err != nil {
		return err
	}

	// For CTAS: insert the SELECT rows.
	if op.SelectSrc != nil {
		ht, err := ctx.Storage.GetTable(op.TableName)
		if err != nil {
			return err
		}
		for {
			t, err := op.SelectSrc.Next()
			if err != nil {
				return err
			}
			if t == nil {
				break
			}
			row := make([]catalog.Value, len(cols))
			for i := range row {
				if i < len(t.Values) {
					row[i] = t.Values[i]
				} else {
					row[i] = catalog.NullValue()
				}
			}
			ht.Insert(storage.Tuple{Values: row})
		}
	}

	op.emitted = false
	return nil
}

func (op *CreateTableOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true
	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.TextValue(fmt.Sprintf("CREATE TABLE %s", op.TableName))},
		Schema: op.schema,
	}, nil
}

func (op *CreateTableOp) Close() error {
	if op.SelectSrc != nil {
		return op.SelectSrc.Close()
	}
	return nil
}

// DropTableOp drops a table from the catalog and storage.
// When IfExists is true it succeeds silently even if the table does not exist.
type DropTableOp struct {
	TableName string
	IfExists  bool
	schema    []catalog.Column
	emitted   bool
}

func (op *DropTableOp) Schema() []catalog.Column { return op.schema }

func (op *DropTableOp) Open(ctx *exectypes.ExecContext) error {
	op.schema = []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
	ok := ctx.Catalog.Drop(op.TableName)
	if !ok && !op.IfExists {
		return fmt.Errorf("table %q does not exist", op.TableName)
	}
	// Also drop storage if the table existed
	if ok && ctx.Storage != nil {
		ctx.Storage.DropTable(op.TableName)
	}
	op.emitted = false
	return nil
}

func (op *DropTableOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true
	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.TextValue(fmt.Sprintf("DROP TABLE %s", op.TableName))},
		Schema: op.schema,
	}, nil
}

func (op *DropTableOp) Close() error { return nil }

// AlterTableOp modifies a table's schema: ADD COLUMN, DROP COLUMN, RENAME TABLE,
// or RENAME COLUMN. It updates both the catalog schema and the heap row values.
type AlterTableOp struct {
	TableName  string
	Action     string
	ColDef     *catalog.Column
	ColName    string
	NewName    string
	DefaultVal ast.Expression
	schema     []catalog.Column
	emitted    bool
}

func (op *AlterTableOp) Schema() []catalog.Column { return op.schema }

func (op *AlterTableOp) Open(ctx *exectypes.ExecContext) error {
	op.schema = []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
	var err error
	switch strings.ToUpper(op.Action) {
	case "ADD":
		if op.ColDef == nil {
			return fmt.Errorf("ALTER TABLE ADD: missing column definition")
		}
		err = ctx.Catalog.AddColumn(op.TableName, *op.ColDef)
		if err == nil && ctx.Storage != nil {
			if ht, htErr := ctx.Storage.GetTable(op.TableName); htErr == nil {
				if op.DefaultVal != nil {
					defVal, evalErr := EvalExpr(op.DefaultVal, nil, ctx)
					if evalErr == nil {
						ht.AddColumnDefault(defVal)
					} else {
						ht.AddColumnNulls()
					}
				} else {
					ht.AddColumnNulls()
				}
			}
		}
	case "DROP":
		// Find the column index before removing it from the schema so we can
		// also drop the value from every heap row (prevents misalignment).
		dropIdx := -1
		if tbl, ok := ctx.Catalog.Lookup(op.TableName); ok {
			for i, col := range tbl.Columns {
				if strings.EqualFold(col.Name, op.ColName) {
					dropIdx = i
					break
				}
			}
		}
		err = ctx.Catalog.DropColumn(op.TableName, op.ColName)
		if err == nil && dropIdx >= 0 && ctx.Storage != nil {
			if ht, htErr := ctx.Storage.GetTable(op.TableName); htErr == nil {
				ht.DropColumnValues(dropIdx)
			}
		}
	case "RENAME":
		err = ctx.Catalog.RenameTable(op.TableName, op.NewName)
		if err == nil && ctx.Storage != nil {
			_ = ctx.Storage.RenameTable(op.TableName, op.NewName)
		}
	case "RENAME_COLUMN":
		err = ctx.Catalog.RenameColumn(op.TableName, op.ColName, op.NewName)
	default:
		return fmt.Errorf("ALTER TABLE: unknown action %q", op.Action)
	}
	if err != nil {
		return err
	}
	op.emitted = false
	return nil
}

func (op *AlterTableOp) Next() (*exectypes.Tuple, error) {
	if op.emitted {
		return nil, nil
	}
	op.emitted = true
	return &exectypes.Tuple{
		Values: []catalog.Value{catalog.TextValue(fmt.Sprintf("ALTER TABLE %s %s", op.TableName, op.Action))},
		Schema: op.schema,
	}, nil
}

func (op *AlterTableOp) Close() error { return nil }
