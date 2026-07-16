package operators

import (
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// Projection evaluates a list of expressions over each child tuple.
type Projection struct {
	ctx       *exectypes.ExecContext
	Child       Operator
	Expressions []ast.Expression
	Aliases     []string // parallel to Expressions; "" means use printExpr
	schema      []catalog.Column
}

func (op *Projection) Schema() []catalog.Column { return op.schema }

func (op *Projection) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	if err := op.Child.Open(ctx); err != nil {
		return err
	}
	childSchema := op.Child.Schema()
	op.schema = make([]catalog.Column, len(op.Expressions))
	for i, expr := range op.Expressions {
		name := op.Aliases[i]
		if name == "" {
			name = expressionName(expr)
		}
		dt := inferColumnType(expr, childSchema)
		op.schema[i] = catalog.Column{Name: name, Type: dt, Index: i}
	}
	return nil
}

func (op *Projection) Next() (*exectypes.Tuple, error) {
	tuple, err := op.Child.Next()
	if err != nil {
		return nil, err
	}
	if tuple == nil {
		return nil, nil
	}

	vals := make([]catalog.Value, len(op.Expressions))
	for i, expr := range op.Expressions {
		v, err := EvalExpr(expr, tuple, op.ctx)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return &exectypes.Tuple{Values: vals, Schema: op.schema}, nil
}

func (op *Projection) Close() error {
	return op.Child.Close()
}

func expressionName(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.ColumnRef:
		return e.Column
	case *ast.AliasExpr:
		return e.Alias
	default:
		return ast.PrintExpr(expr)
	}
}

func inferColumnType(expr ast.Expression, schema []catalog.Column) catalog.DataType {
	switch e := expr.(type) {
	case *ast.IntLiteral:
		return catalog.TypeInt
	case *ast.FloatLiteral:
		return catalog.TypeFloat
	case *ast.StringLiteral:
		return catalog.TypeText
	case *ast.BoolLiteral:
		return catalog.TypeBool
	case *ast.ColumnRef:
		name := e.ResolvedTable + "." + e.Column
		for _, col := range schema {
			if col.Name == name || col.Name == e.Column || strings.HasSuffix(col.Name, "."+e.Column) {
				return col.Type
			}
		}
		return catalog.TypeNull
	case *ast.AliasExpr:
		return inferColumnType(e.Expr, schema)
	case *ast.FunctionCall:
		switch strings.ToUpper(e.Name) {
		case "COUNT":
			return catalog.TypeInt
		case "SUM", "AVG":
			return catalog.TypeFloat
		case "MIN", "MAX":
			if len(e.Args) > 0 {
				return inferColumnType(e.Args[0], schema)
			}
		}
		return catalog.TypeNull
	case *ast.BinaryExpr:
		lt := inferColumnType(e.Left, schema)
		rt := inferColumnType(e.Right, schema)
		if lt == catalog.TypeFloat || rt == catalog.TypeFloat {
			return catalog.TypeFloat
		}
		if lt != catalog.TypeNull {
			return lt
		}
		return rt
	}
	return catalog.TypeNull
}
