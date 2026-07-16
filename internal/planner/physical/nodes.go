package physical

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
)

// nodeCounter generates unique node IDs; atomic to avoid data races under concurrent requests.
var nodeCounter int64

func nextID() string {
	return fmt.Sprintf("node-%d", atomic.AddInt64(&nodeCounter, 1))
}

func resetID() { atomic.StoreInt64(&nodeCounter, 0) }

// -----------------------------------------------------------------------
// SeqScan
// -----------------------------------------------------------------------

type SeqScan struct {
	TableName string
	Alias     string
	Table     *catalog.Table
}

func (n *SeqScan) Children() []Plan { return nil }

func (n *SeqScan) Schema() []catalog.Column {
	alias := n.Alias
	if alias == "" {
		alias = n.TableName
	}
	cols := make([]catalog.Column, len(n.Table.Columns))
	for i, col := range n.Table.Columns {
		cols[i] = catalog.Column{Name: alias + "." + col.Name, Type: col.Type, Nullable: col.Nullable, PK: col.PK, Index: i}
	}
	return cols
}

func (n *SeqScan) String() string {
	if n.Alias != "" && n.Alias != n.TableName {
		return fmt.Sprintf("SeqScan(%s AS %s)", n.TableName, n.Alias)
	}
	return fmt.Sprintf("SeqScan(%s)", n.TableName)
}

func (n *SeqScan) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":            nextID(),
		"type":          "SeqScan",
		"estimatedRows": 0,
		"estimatedCost": 0.0,
		"attributes":    map[string]interface{}{"table": n.TableName, "alias": n.Alias},
		"children":      []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Filter
// -----------------------------------------------------------------------

type Filter struct {
	Child     Plan
	Predicate ast.Expression
}

func (n *Filter) Children() []Plan           { return []Plan{n.Child} }
func (n *Filter) Schema() []catalog.Column   { return n.Child.Schema() }
func (n *Filter) String() string             { return fmt.Sprintf("Filter(%s)", printExpr(n.Predicate)) }

func (n *Filter) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   nextID(),
		"type": "Filter",
		"attributes": map[string]interface{}{
			"predicate": printExpr(n.Predicate),
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// Projection
// -----------------------------------------------------------------------

type Projection struct {
	Child       Plan
	Expressions []ast.Expression
	Aliases     []string
}

func (n *Projection) Children() []Plan { return []Plan{n.Child} }

func (n *Projection) Schema() []catalog.Column {
	cols := make([]catalog.Column, len(n.Expressions))
	childSchema := n.Child.Schema()
	for i, expr := range n.Expressions {
		name := n.Aliases[i]
		if name == "" {
			name = printExpr(expr)
		}
		dt := inferType(expr, childSchema)
		cols[i] = catalog.Column{Name: name, Type: dt, Index: i}
	}
	return cols
}

func (n *Projection) String() string {
	parts := make([]string, len(n.Expressions))
	for i, e := range n.Expressions {
		s := printExpr(e)
		if n.Aliases[i] != "" {
			s += " AS " + n.Aliases[i]
		}
		parts[i] = s
	}
	return fmt.Sprintf("Projection([%s])", strings.Join(parts, ", "))
}

func (n *Projection) ToJSON() map[string]interface{} {
	exprs := make([]string, len(n.Expressions))
	for i, e := range n.Expressions {
		exprs[i] = printExpr(e)
	}
	return map[string]interface{}{
		"id":   nextID(),
		"type": "Projection",
		"attributes": map[string]interface{}{
			"expressions": exprs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// HashJoin
// -----------------------------------------------------------------------

type HashJoin struct {
	Left      Plan
	Right     Plan // build side (inner)
	JoinType  JoinType
	Condition ast.Expression
}

type JoinType int

const (
	InnerJoin JoinType = iota
	LeftJoin
	RightJoin
	CrossJoin
	FullJoin
)

func (jt JoinType) String() string {
	switch jt {
	case InnerJoin:
		return "INNER"
	case LeftJoin:
		return "LEFT"
	case RightJoin:
		return "RIGHT"
	case CrossJoin:
		return "CROSS"
	case FullJoin:
		return "FULL"
	default:
		return "UNKNOWN"
	}
}

func (n *HashJoin) Children() []Plan { return []Plan{n.Left, n.Right} }

func (n *HashJoin) Schema() []catalog.Column {
	left := n.Left.Schema()
	right := n.Right.Schema()
	result := make([]catalog.Column, len(left)+len(right))
	copy(result, left)
	for i, col := range right {
		result[len(left)+i] = catalog.Column{Name: col.Name, Type: col.Type, Nullable: col.Nullable, Index: len(left) + i}
	}
	return result
}

func (n *HashJoin) String() string {
	return fmt.Sprintf("HashJoin(%s, ON: %s)", n.JoinType, printExpr(n.Condition))
}

func (n *HashJoin) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   nextID(),
		"type": "HashJoin",
		"attributes": map[string]interface{}{
			"joinType":  n.JoinType.String(),
			"condition": printExpr(n.Condition),
			"algorithm": "HashJoin",
			"buildSide": "right",
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// NestedLoopJoin
// -----------------------------------------------------------------------

type NestedLoopJoin struct {
	Left      Plan
	Right     Plan
	JoinType  JoinType
	Condition ast.Expression
}

func (n *NestedLoopJoin) Children() []Plan { return []Plan{n.Left, n.Right} }

func (n *NestedLoopJoin) Schema() []catalog.Column {
	left := n.Left.Schema()
	right := n.Right.Schema()
	result := make([]catalog.Column, len(left)+len(right))
	copy(result, left)
	for i, col := range right {
		result[len(left)+i] = catalog.Column{Name: col.Name, Type: col.Type, Nullable: col.Nullable, Index: len(left) + i}
	}
	return result
}

func (n *NestedLoopJoin) String() string {
	return fmt.Sprintf("NestedLoopJoin(%s, ON: %s)", n.JoinType, printExpr(n.Condition))
}

func (n *NestedLoopJoin) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   nextID(),
		"type": "NestedLoopJoin",
		"attributes": map[string]interface{}{
			"joinType":  n.JoinType.String(),
			"condition": printExpr(n.Condition),
			"algorithm": "NestedLoop",
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// SortMergeJoin
// -----------------------------------------------------------------------

type SortMergeJoin struct {
	Left      Plan
	Right     Plan
	JoinType  JoinType
	Condition ast.Expression
}

func (n *SortMergeJoin) Children() []Plan { return []Plan{n.Left, n.Right} }

func (n *SortMergeJoin) Schema() []catalog.Column {
	left := n.Left.Schema()
	right := n.Right.Schema()
	result := make([]catalog.Column, len(left)+len(right))
	copy(result, left)
	for i, col := range right {
		result[len(left)+i] = catalog.Column{Name: col.Name, Type: col.Type, Nullable: col.Nullable, Index: len(left) + i}
	}
	return result
}

func (n *SortMergeJoin) String() string {
	return fmt.Sprintf("SortMergeJoin(%s, ON: %s)", n.JoinType, printExpr(n.Condition))
}

func (n *SortMergeJoin) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   nextID(),
		"type": "SortMergeJoin",
		"attributes": map[string]interface{}{
			"joinType":  n.JoinType.String(),
			"condition": printExpr(n.Condition),
			"algorithm": "SortMerge",
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// HashAggregate
// -----------------------------------------------------------------------

type AggExpr struct {
	Function string
	Arg      ast.Expression
	StarArg  bool
	Distinct bool
	Alias    string
}

type HashAggregate struct {
	Child   Plan
	GroupBy []ast.Expression
	Aggs    []AggExpr
}

func (n *HashAggregate) Children() []Plan { return []Plan{n.Child} }

func (n *HashAggregate) Schema() []catalog.Column {
	childSchema := n.Child.Schema()
	var cols []catalog.Column
	for i, g := range n.GroupBy {
		name := printExpr(g)
		dt := inferType(g, childSchema)
		cols = append(cols, catalog.Column{Name: name, Type: dt, Index: i})
	}
	for i, agg := range n.Aggs {
		name := agg.Alias
		if name == "" {
			if agg.StarArg {
				name = fmt.Sprintf("%s(*)", agg.Function)
			} else {
				name = fmt.Sprintf("%s(%s)", agg.Function, printExpr(agg.Arg))
			}
		}
		cols = append(cols, catalog.Column{Name: name, Type: aggType(agg.Function), Index: len(n.GroupBy) + i})
	}
	return cols
}

func (n *HashAggregate) String() string {
	groups := make([]string, len(n.GroupBy))
	for i, g := range n.GroupBy {
		groups[i] = printExpr(g)
	}
	aggs := make([]string, len(n.Aggs))
	for i, a := range n.Aggs {
		if a.StarArg {
			aggs[i] = fmt.Sprintf("%s(*)", a.Function)
		} else {
			aggs[i] = fmt.Sprintf("%s(%s)", a.Function, printExpr(a.Arg))
		}
	}
	return fmt.Sprintf("HashAggregate(groupBy=[%s], aggs=[%s])", strings.Join(groups, ", "), strings.Join(aggs, ", "))
}

func (n *HashAggregate) ToJSON() map[string]interface{} {
	groups := make([]string, len(n.GroupBy))
	for i, g := range n.GroupBy {
		groups[i] = printExpr(g)
	}
	aggs := make([]string, len(n.Aggs))
	for i, a := range n.Aggs {
		if a.StarArg {
			aggs[i] = fmt.Sprintf("%s(*)", a.Function)
		} else {
			aggs[i] = fmt.Sprintf("%s(%s)", a.Function, printExpr(a.Arg))
		}
	}
	return map[string]interface{}{
		"id":   nextID(),
		"type": "HashAggregate",
		"attributes": map[string]interface{}{
			"groupBy":    groups,
			"aggregates": aggs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// Sort
// -----------------------------------------------------------------------

type SortSpec struct {
	Expr           ast.Expression
	Ascending      bool
	NullsFirst     bool
	NullsSpecified bool
}

type Sort struct {
	Child     Plan
	SortSpecs []SortSpec
}

func (n *Sort) Children() []Plan           { return []Plan{n.Child} }
func (n *Sort) Schema() []catalog.Column   { return n.Child.Schema() }

func (n *Sort) String() string {
	specs := make([]string, len(n.SortSpecs))
	for i, s := range n.SortSpecs {
		dir := "ASC"
		if !s.Ascending {
			dir = "DESC"
		}
		specs[i] = fmt.Sprintf("%s %s", printExpr(s.Expr), dir)
	}
	return fmt.Sprintf("Sort([%s])", strings.Join(specs, ", "))
}

func (n *Sort) ToJSON() map[string]interface{} {
	specs := make([]string, len(n.SortSpecs))
	for i, s := range n.SortSpecs {
		dir := "ASC"
		if !s.Ascending {
			dir = "DESC"
		}
		specs[i] = fmt.Sprintf("%s %s", printExpr(s.Expr), dir)
	}
	return map[string]interface{}{
		"id":   nextID(),
		"type": "Sort",
		"attributes": map[string]interface{}{
			"sortSpecs": specs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// Limit
// -----------------------------------------------------------------------

type Limit struct {
	Child  Plan
	Count  ast.Expression
	Offset ast.Expression
}

func (n *Limit) Children() []Plan         { return []Plan{n.Child} }
func (n *Limit) Schema() []catalog.Column { return n.Child.Schema() }

func (n *Limit) String() string {
	if n.Offset != nil {
		return fmt.Sprintf("Limit(count=%s, offset=%s)", printExpr(n.Count), printExpr(n.Offset))
	}
	return fmt.Sprintf("Limit(count=%s)", printExpr(n.Count))
}

func (n *Limit) ToJSON() map[string]interface{} {
	attrs := map[string]interface{}{"count": printExpr(n.Count)}
	if n.Offset != nil {
		attrs["offset"] = printExpr(n.Offset)
	}
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Limit",
		"attributes": attrs,
		"children":   []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// SetOp (UNION / INTERSECT / EXCEPT)
// -----------------------------------------------------------------------

type SetOp struct {
	Op    string // "UNION", "INTERSECT", "EXCEPT"
	All   bool
	Left  Plan
	Right Plan
}

func (n *SetOp) Children() []Plan         { return []Plan{n.Left, n.Right} }
func (n *SetOp) Schema() []catalog.Column { return n.Left.Schema() }

func (n *SetOp) String() string {
	all := ""
	if n.All {
		all = " ALL"
	}
	return fmt.Sprintf("%s%s", n.Op, all)
}

func (n *SetOp) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   nextID(),
		"type": n.Op,
		"attributes": map[string]interface{}{
			"all": n.All,
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// Insert
// -----------------------------------------------------------------------

type Insert struct {
	TableName string
	Table     *catalog.Table
	Columns   []string
	ValueRows [][]ast.Expression
	SelectSrc Plan // non-nil for INSERT ... SELECT
}

func (n *Insert) Children() []Plan {
	if n.SelectSrc != nil {
		return []Plan{n.SelectSrc}
	}
	return nil
}
func (n *Insert) Schema() []catalog.Column { return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}} }
func (n *Insert) String() string           { return fmt.Sprintf("Insert(%s)", n.TableName) }

func (n *Insert) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Insert",
		"attributes": map[string]interface{}{"table": n.TableName, "columns": n.Columns},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Update / Delete
// -----------------------------------------------------------------------

type UpdateAssign struct {
	Column string
	Value  ast.Expression
}

type Update struct {
	TableName string
	Table     *catalog.Table
	Assigns   []UpdateAssign
	Where     ast.Expression
}

func (n *Update) Children() []Plan         { return nil }
func (n *Update) Schema() []catalog.Column {
	return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}}
}
func (n *Update) String() string { return fmt.Sprintf("Update(%s)", n.TableName) }
func (n *Update) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Update",
		"attributes": map[string]interface{}{"table": n.TableName},
		"children":   []interface{}{},
	}
}

type Delete struct {
	TableName string
	Table     *catalog.Table
	Where     ast.Expression
}

func (n *Delete) Children() []Plan         { return nil }
func (n *Delete) Schema() []catalog.Column {
	return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}}
}
func (n *Delete) String() string { return fmt.Sprintf("Delete(%s)", n.TableName) }
func (n *Delete) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Delete",
		"attributes": map[string]interface{}{"table": n.TableName},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Explain — wraps a plan for EXPLAIN [ANALYZE].
// -----------------------------------------------------------------------

type Explain struct {
	Inner   Plan
	Analyze bool
}

func (n *Explain) Children() []Plan         { return []Plan{n.Inner} }
func (n *Explain) Schema() []catalog.Column {
	return []catalog.Column{{Name: "Plan", Type: catalog.TypeText, Index: 0}}
}
func (n *Explain) String() string {
	if n.Analyze {
		return "Explain(ANALYZE)"
	}
	return "Explain"
}
func (n *Explain) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": nextID(), "type": "Explain",
		"attributes": map[string]interface{}{"analyze": n.Analyze},
		"children":   []interface{}{n.Inner.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// Window — computes window functions over a materialized child.
// -----------------------------------------------------------------------

// WindowExpr pairs a window function AST with its output alias.
type WindowExpr struct {
	Expr  *ast.WindowFuncExpr
	Alias string
}

type Window struct {
	Child   Plan
	Windows []WindowExpr
}

func (n *Window) Children() []Plan { return []Plan{n.Child} }
func (n *Window) Schema() []catalog.Column {
	base := n.Child.Schema()
	result := make([]catalog.Column, len(base)+len(n.Windows))
	copy(result, base)
	for i, w := range n.Windows {
		name := w.Alias
		if name == "" {
			name = strings.ToLower(w.Expr.Func.Name)
		}
		result[len(base)+i] = catalog.Column{Name: name, Type: catalog.TypeInt, Index: len(base) + i}
	}
	return result
}
func (n *Window) String() string { return "Window" }
func (n *Window) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": nextID(), "type": "Window",
		"attributes": map[string]interface{}{}, "children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// CreateTable / DropTable / AlterTable DDL nodes
// -----------------------------------------------------------------------

type CreateTable struct {
	TableName string
	Columns   []*ast.ColumnDef
	SelectSrc Plan // non-nil for CREATE TABLE ... AS SELECT
}

func (n *CreateTable) Children() []Plan {
	if n.SelectSrc != nil {
		return []Plan{n.SelectSrc}
	}
	return nil
}
func (n *CreateTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *CreateTable) String() string { return fmt.Sprintf("CreateTable(%s)", n.TableName) }
func (n *CreateTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": nextID(), "type": "CreateTable",
		"attributes": map[string]interface{}{"table": n.TableName}, "children": []interface{}{},
	}
}

type DropTable struct {
	TableName string
	IfExists  bool
}

func (n *DropTable) Children() []Plan         { return nil }
func (n *DropTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *DropTable) String() string { return fmt.Sprintf("DropTable(%s)", n.TableName) }
func (n *DropTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": nextID(), "type": "DropTable",
		"attributes": map[string]interface{}{"table": n.TableName}, "children": []interface{}{},
	}
}

type AlterTable struct {
	TableName  string
	Action     string
	ColDef     *catalog.Column
	ColName    string
	NewName    string
	DefaultVal ast.Expression
}

func (n *AlterTable) Children() []Plan         { return nil }
func (n *AlterTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *AlterTable) String() string {
	return fmt.Sprintf("AlterTable(%s %s)", n.TableName, n.Action)
}
func (n *AlterTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": nextID(), "type": "AlterTable",
		"attributes": map[string]interface{}{"table": n.TableName, "action": n.Action},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Empty (placeholder for EmptyRelation)
// -----------------------------------------------------------------------

type Empty struct {
	Cols []catalog.Column
}

func (n *Empty) Children() []Plan         { return nil }
func (n *Empty) Schema() []catalog.Column { return n.Cols }
func (n *Empty) String() string           { return "Empty" }
func (n *Empty) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Empty",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// ConstantScan — produces exactly one row with no columns.
// Used as the implicit FROM for SELECT without a FROM clause.
// -----------------------------------------------------------------------

type ConstantScan struct{}

// -----------------------------------------------------------------------
// Distinct — deduplicates its child's output (SELECT DISTINCT).
// -----------------------------------------------------------------------

type Distinct struct {
	Child Plan
}

func (n *Distinct) Children() []Plan         { return []Plan{n.Child} }
func (n *Distinct) Schema() []catalog.Column { return n.Child.Schema() }
func (n *Distinct) String() string           { return "Distinct" }
func (n *Distinct) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "Distinct",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{n.Child.ToJSON()},
	}
}

func (n *ConstantScan) Children() []Plan         { return nil }
func (n *ConstantScan) Schema() []catalog.Column { return nil }
func (n *ConstantScan) String() string           { return "ConstantScan" }
func (n *ConstantScan) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         nextID(),
		"type":       "ConstantScan",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func printExpr(e ast.Expression) string {
	if e == nil {
		return ""
	}
	return ast.PrintExpr(e)
}

func inferType(expr ast.Expression, schema []catalog.Column) catalog.DataType {
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
			if col.Name == name || strings.HasSuffix(col.Name, "."+e.Column) {
				return col.Type
			}
		}
		return catalog.TypeNull
	case *ast.FunctionCall:
		return aggType(e.Name)
	case *ast.AliasExpr:
		return inferType(e.Expr, schema)
	case *ast.BinaryExpr:
		lt := inferType(e.Left, schema)
		rt := inferType(e.Right, schema)
		if lt == catalog.TypeFloat || rt == catalog.TypeFloat {
			return catalog.TypeFloat
		}
		return lt
	}
	return catalog.TypeNull
}

func aggType(fn string) catalog.DataType {
	switch strings.ToUpper(fn) {
	case "COUNT":
		return catalog.TypeInt
	case "SUM", "AVG":
		return catalog.TypeFloat
	case "MIN", "MAX":
		return catalog.TypeText // conservative fallback; actual type matches input column
	default:
		return catalog.TypeNull
	}
}
