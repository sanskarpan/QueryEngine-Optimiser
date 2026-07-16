package ast

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/lexer"
)

// Print returns a human-readable string representation of any AST node.
func Print(node interface{}) string {
	p := &printer{}
	p.print(node, 0)
	return strings.TrimRight(p.buf.String(), "\n")
}

type printer struct {
	buf strings.Builder
}

func (p *printer) write(s string) {
	p.buf.WriteString(s)
}

func (p *printer) indent(depth int) string {
	return strings.Repeat("  ", depth)
}

func (p *printer) print(node interface{}, depth int) {
	ind := p.indent(depth)
	switch n := node.(type) {
	case *SelectStatement:
		p.write(ind + "SelectStatement\n")
		if n.Distinct {
			p.write(ind + "  DISTINCT\n")
		}
		p.write(ind + "  Columns:\n")
		for _, col := range n.Columns {
			p.print(col, depth+2)
		}
		if n.From != nil {
			p.write(ind + "  From:\n")
			p.printTableRef(n.From, depth+2)
		}
		for _, j := range n.Joins {
			p.write(fmt.Sprintf("%s  %s JOIN:\n", ind, j.JoinType))
			p.printTableRef(j.Table, depth+2)
			if j.Condition != nil {
				p.write(ind + "    ON:\n")
				p.print(j.Condition, depth+3)
			}
		}
		if n.Where != nil {
			p.write(ind + "  Where:\n")
			p.print(n.Where, depth+2)
		}
		if len(n.GroupBy) > 0 {
			p.write(ind + "  GroupBy:\n")
			for _, g := range n.GroupBy {
				p.print(g, depth+2)
			}
		}
		if n.Having != nil {
			p.write(ind + "  Having:\n")
			p.print(n.Having, depth+2)
		}
		if len(n.OrderBy) > 0 {
			p.write(ind + "  OrderBy:\n")
			for _, s := range n.OrderBy {
				dir := "ASC"
				if !s.Ascending {
					dir = "DESC"
				}
				p.write(fmt.Sprintf("%s    %s\n", ind, dir))
				p.print(s.Expr, depth+3)
			}
		}
		if n.Limit != nil {
			p.write(ind + "  Limit:\n")
			p.print(n.Limit, depth+2)
		}
		if n.Offset != nil {
			p.write(ind + "  Offset:\n")
			p.print(n.Offset, depth+2)
		}

	case *CreateTableStatement:
		p.write(fmt.Sprintf("%sCreateTable(%s)\n", ind, n.Name))
		for _, col := range n.Columns {
			flags := ""
			if col.NotNull {
				flags += " NOT NULL"
			}
			if col.PrimaryKey {
				flags += " PRIMARY KEY"
			}
			p.write(fmt.Sprintf("%s  Column(%s %s%s)\n", ind, col.Name, col.TypeName, flags))
		}

	case *InsertStatement:
		p.write(fmt.Sprintf("%sInsert(%s)\n", ind, n.Table))
		p.write(fmt.Sprintf("%s  Columns: %s\n", ind, strings.Join(n.Columns, ", ")))
		p.write(ind + "  Values:\n")
		for _, v := range n.Values {
			p.print(v, depth+2)
		}

	case *StarExpr:
		p.write(ind + "*\n")

	case *AliasExpr:
		p.write(fmt.Sprintf("%sAlias(%s)\n", ind, n.Alias))
		p.print(n.Expr, depth+1)

	case *ColumnRef:
		if n.Table != "" {
			p.write(fmt.Sprintf("%sColumnRef(%s.%s)\n", ind, n.Table, n.Column))
		} else {
			p.write(fmt.Sprintf("%sColumnRef(%s)\n", ind, n.Column))
		}

	case *IntLiteral:
		p.write(fmt.Sprintf("%sInt(%d)\n", ind, n.Value))

	case *FloatLiteral:
		p.write(fmt.Sprintf("%sFloat(%g)\n", ind, n.Value))

	case *StringLiteral:
		p.write(fmt.Sprintf("%sString(%q)\n", ind, n.Value))

	case *BoolLiteral:
		p.write(fmt.Sprintf("%sBool(%v)\n", ind, n.Value))

	case *NullLiteral:
		p.write(ind + "NULL\n")

	case *BinaryExpr:
		p.write(fmt.Sprintf("%sBinaryExpr(%s)\n", ind, n.Op.Literal))
		p.print(n.Left, depth+1)
		p.print(n.Right, depth+1)

	case *UnaryExpr:
		p.write(fmt.Sprintf("%sUnaryExpr(%s)\n", ind, n.Op.Literal))
		p.print(n.Expr, depth+1)

	case *FunctionCall:
		if n.StarArg {
			p.write(fmt.Sprintf("%sFunction(%s(*))\n", ind, n.Name))
		} else {
			p.write(fmt.Sprintf("%sFunction(%s)\n", ind, n.Name))
			for _, arg := range n.Args {
				p.print(arg, depth+1)
			}
		}

	case *CaseExpr:
		p.write(ind + "Case\n")
		if n.Operand != nil {
			p.write(ind + "  Operand:\n")
			p.print(n.Operand, depth+2)
		}
		for _, w := range n.Whens {
			p.write(ind + "  When:\n")
			p.print(w.Condition, depth+2)
			p.write(ind + "  Then:\n")
			p.print(w.Result, depth+2)
		}
		if n.ElseExpr != nil {
			p.write(ind + "  Else:\n")
			p.print(n.ElseExpr, depth+2)
		}

	case *InExpr:
		neg := ""
		if n.Negated {
			neg = "NOT "
		}
		p.write(fmt.Sprintf("%s%sIn\n", ind, neg))
		p.print(n.Expr, depth+1)
		if n.Subquery != nil {
			p.print(n.Subquery, depth+1)
		} else {
			for _, item := range n.List {
				p.print(item, depth+1)
			}
		}

	case *BetweenExpr:
		neg := ""
		if n.Negated {
			neg = "NOT "
		}
		p.write(fmt.Sprintf("%s%sBetween\n", ind, neg))
		p.print(n.Expr, depth+1)
		p.write(ind + "  Low:\n")
		p.print(n.Low, depth+2)
		p.write(ind + "  High:\n")
		p.print(n.High, depth+2)

	case *IsNullExpr:
		if n.Negated {
			p.write(ind + "IsNotNull\n")
		} else {
			p.write(ind + "IsNull\n")
		}
		p.print(n.Expr, depth+1)

	case *SubqueryExpr:
		p.write(ind + "Subquery\n")
		p.print(n.Select, depth+1)

	case *ExistsExpr:
		if n.Negated {
			p.write(ind + "NotExists\n")
		} else {
			p.write(ind + "Exists\n")
		}
		p.print(n.Subquery, depth+1)

	default:
		p.write(fmt.Sprintf("%s<unknown: %T>\n", ind, node))
	}
}

func (p *printer) printTableRef(ref *TableRef, depth int) {
	ind := p.indent(depth)
	if ref.Subquery != nil {
		alias := ref.Alias
		p.write(fmt.Sprintf("%sSubqueryRef(alias=%s)\n", ind, alias))
		p.print(ref.Subquery, depth+1)
	} else {
		alias := ref.Alias
		if alias == "" {
			p.write(fmt.Sprintf("%sTableRef(%s)\n", ind, ref.Name))
		} else {
			p.write(fmt.Sprintf("%sTableRef(%s AS %s)\n", ind, ref.Name, alias))
		}
	}
}

// PrintExpr returns a compact single-line string for an expression.
// Useful for plan printing.
func PrintExpr(expr Expression) string {
	if expr == nil {
		return "<nil>"
	}
	switch e := expr.(type) {
	case *StarExpr:
		return "*"
	case *AliasExpr:
		return fmt.Sprintf("%s AS %s", PrintExpr(e.Expr), e.Alias)
	case *ColumnRef:
		if e.Table != "" {
			return e.Table + "." + e.Column
		}
		return e.Column
	case *IntLiteral:
		return fmt.Sprintf("%d", e.Value)
	case *FloatLiteral:
		return fmt.Sprintf("%g", e.Value)
	case *StringLiteral:
		return fmt.Sprintf("'%s'", e.Value)
	case *BoolLiteral:
		if e.Value {
			return "TRUE"
		}
		return "FALSE"
	case *NullLiteral:
		return "NULL"
	case *BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", PrintExpr(e.Left), opString(e.Op), PrintExpr(e.Right))
	case *UnaryExpr:
		return fmt.Sprintf("(%s %s)", e.Op.Literal, PrintExpr(e.Expr))
	case *FunctionCall:
		if e.StarArg {
			return fmt.Sprintf("%s(*)", e.Name)
		}
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = PrintExpr(a)
		}
		return fmt.Sprintf("%s(%s)", e.Name, strings.Join(args, ", "))
	case *IsNullExpr:
		if e.Negated {
			return fmt.Sprintf("%s IS NOT NULL", PrintExpr(e.Expr))
		}
		return fmt.Sprintf("%s IS NULL", PrintExpr(e.Expr))
	case *InExpr:
		neg := ""
		if e.Negated {
			neg = "NOT "
		}
		if e.Subquery != nil {
			return fmt.Sprintf("%s %sIN (subquery)", PrintExpr(e.Expr), neg)
		}
		items := make([]string, len(e.List))
		for i, item := range e.List {
			items[i] = PrintExpr(item)
		}
		return fmt.Sprintf("%s %sIN (%s)", PrintExpr(e.Expr), neg, strings.Join(items, ", "))
	case *BetweenExpr:
		neg := ""
		if e.Negated {
			neg = "NOT "
		}
		return fmt.Sprintf("%s %sBETWEEN %s AND %s", PrintExpr(e.Expr), neg, PrintExpr(e.Low), PrintExpr(e.High))
	case *SubqueryExpr:
		return "(subquery)"
	case *ExistsExpr:
		if e.Negated {
			return "NOT EXISTS (subquery)"
		}
		return "EXISTS (subquery)"
	case *CaseExpr:
		return "CASE..."
	default:
		return fmt.Sprintf("<%T>", expr)
	}
}

func opString(tok lexer.Token) string {
	return tok.Literal
}
