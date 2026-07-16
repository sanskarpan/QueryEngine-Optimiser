package logical

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
)

// -----------------------------------------------------------------------
// LogicalScan
// -----------------------------------------------------------------------

// LogicalScan reads all rows from a base table.
type LogicalScan struct {
	TableName string
	Alias     string
	Table     *catalog.Table
}

func (n *LogicalScan) Children() []Plan { return nil }

func (n *LogicalScan) Schema() []catalog.Column {
	cols := make([]catalog.Column, len(n.Table.Columns))
	alias := n.Alias
	if alias == "" {
		alias = n.TableName
	}
	for i, col := range n.Table.Columns {
		cols[i] = catalog.Column{
			Name:     alias + "." + col.Name,
			Type:     col.Type,
			Nullable: col.Nullable,
			PK:       col.PK,
			Index:    i,
		}
	}
	return cols
}

func (n *LogicalScan) String() string {
	if n.Alias != "" && n.Alias != n.TableName {
		return fmt.Sprintf("LogicalScan(%s AS %s)", n.TableName, n.Alias)
	}
	return fmt.Sprintf("LogicalScan(%s)", n.TableName)
}

func (n *LogicalScan) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Scan",
		"attributes": map[string]interface{}{
			"table": n.TableName,
			"alias": n.Alias,
		},
		"children": []interface{}{},
	}
}

// -----------------------------------------------------------------------
// LogicalFilter
// -----------------------------------------------------------------------

// LogicalFilter applies a predicate to its child.
type LogicalFilter struct {
	Child     Plan
	Predicate ast.Expression
}

func (n *LogicalFilter) Children() []Plan { return []Plan{n.Child} }

func (n *LogicalFilter) Schema() []catalog.Column { return n.Child.Schema() }

func (n *LogicalFilter) String() string {
	return fmt.Sprintf("LogicalFilter(%s)", ast.PrintExpr(n.Predicate))
}

func (n *LogicalFilter) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Filter",
		"attributes": map[string]interface{}{
			"predicate": ast.PrintExpr(n.Predicate),
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalProject
// -----------------------------------------------------------------------

// LogicalProject selects and transforms columns.
type LogicalProject struct {
	Child       Plan
	Expressions []ast.Expression
	Aliases     []string // parallel slice; "" if no alias
}

func (n *LogicalProject) Children() []Plan { return []Plan{n.Child} }

func (n *LogicalProject) Schema() []catalog.Column {
	cols := make([]catalog.Column, len(n.Expressions))
	for i, expr := range n.Expressions {
		name := n.Aliases[i]
		if name == "" {
			name = ast.PrintExpr(expr)
		}
		dt := inferExprType(expr, n.Child.Schema())
		cols[i] = catalog.Column{Name: name, Type: dt, Index: i}
	}
	return cols
}

func (n *LogicalProject) String() string {
	parts := make([]string, len(n.Expressions))
	for i, e := range n.Expressions {
		s := ast.PrintExpr(e)
		if n.Aliases[i] != "" {
			s += " AS " + n.Aliases[i]
		}
		parts[i] = s
	}
	return fmt.Sprintf("LogicalProject([%s])", strings.Join(parts, ", "))
}

func (n *LogicalProject) ToJSON() map[string]interface{} {
	exprs := make([]string, len(n.Expressions))
	for i, e := range n.Expressions {
		exprs[i] = ast.PrintExpr(e)
	}
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Project",
		"attributes": map[string]interface{}{
			"expressions": exprs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalJoin
// -----------------------------------------------------------------------

// JoinType mirrors the AST join type.
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

// LogicalJoin combines two relations.
type LogicalJoin struct {
	Left      Plan
	Right     Plan
	JoinType  JoinType
	Condition ast.Expression // nil for CROSS JOIN
}

func (n *LogicalJoin) Children() []Plan { return []Plan{n.Left, n.Right} }

func (n *LogicalJoin) Schema() []catalog.Column {
	left := n.Left.Schema()
	right := n.Right.Schema()
	rightOff := len(left)
	result := make([]catalog.Column, len(left)+len(right))
	// For LEFT or FULL JOIN, left columns may be non-null but right columns become nullable.
	// For RIGHT or FULL JOIN, right columns may be non-null but left columns become nullable.
	for i, col := range left {
		result[i] = catalog.Column{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable || n.JoinType == RightJoin || n.JoinType == FullJoin,
			PK:       col.PK,
			Index:    i,
		}
	}
	for i, col := range right {
		result[rightOff+i] = catalog.Column{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable || n.JoinType == LeftJoin || n.JoinType == FullJoin,
			PK:       col.PK,
			Index:    rightOff + i,
		}
	}
	return result
}

func (n *LogicalJoin) String() string {
	return fmt.Sprintf("LogicalJoin(%s, ON: %s)", n.JoinType, ast.PrintExpr(n.Condition))
}

func (n *LogicalJoin) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Join",
		"attributes": map[string]interface{}{
			"joinType":  n.JoinType.String(),
			"condition": ast.PrintExpr(n.Condition),
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalAggregate
// -----------------------------------------------------------------------

// AggExpr represents a single aggregate expression.
type AggExpr struct {
	Function string         // COUNT, SUM, AVG, MIN, MAX
	Arg      ast.Expression // nil for COUNT(*)
	StarArg  bool
	Distinct bool // COUNT(DISTINCT col)
	Alias    string
}

// LogicalAggregate groups rows and computes aggregates.
type LogicalAggregate struct {
	Child    Plan
	GroupBy  []ast.Expression
	Aggs     []AggExpr
}

func (n *LogicalAggregate) Children() []Plan { return []Plan{n.Child} }

func (n *LogicalAggregate) Schema() []catalog.Column {
	childSchema := n.Child.Schema()
	var cols []catalog.Column
	// Group-by columns come first
	for i, g := range n.GroupBy {
		name := ast.PrintExpr(g)
		dt := inferExprType(g, childSchema)
		cols = append(cols, catalog.Column{Name: name, Type: dt, Index: i})
	}
	// Then aggregate results
	for i, agg := range n.Aggs {
		name := agg.Alias
		if name == "" {
			if agg.StarArg {
				name = fmt.Sprintf("%s(*)", agg.Function)
			} else {
				name = fmt.Sprintf("%s(%s)", agg.Function, ast.PrintExpr(agg.Arg))
			}
		}
		dt := aggResultType(agg.Function)
		cols = append(cols, catalog.Column{Name: name, Type: dt, Index: len(n.GroupBy) + i})
	}
	return cols
}

func (n *LogicalAggregate) String() string {
	groups := make([]string, len(n.GroupBy))
	for i, g := range n.GroupBy {
		groups[i] = ast.PrintExpr(g)
	}
	aggs := make([]string, len(n.Aggs))
	for i, a := range n.Aggs {
		if a.StarArg {
			aggs[i] = fmt.Sprintf("%s(*)", a.Function)
		} else {
			aggs[i] = fmt.Sprintf("%s(%s)", a.Function, ast.PrintExpr(a.Arg))
		}
	}
	return fmt.Sprintf("LogicalAggregate(groupBy=[%s], aggs=[%s])",
		strings.Join(groups, ", "), strings.Join(aggs, ", "))
}

func (n *LogicalAggregate) ToJSON() map[string]interface{} {
	groups := make([]string, len(n.GroupBy))
	for i, g := range n.GroupBy {
		groups[i] = ast.PrintExpr(g)
	}
	aggs := make([]string, len(n.Aggs))
	for i, a := range n.Aggs {
		if a.StarArg {
			aggs[i] = fmt.Sprintf("%s(*)", a.Function)
		} else {
			aggs[i] = fmt.Sprintf("%s(%s)", a.Function, ast.PrintExpr(a.Arg))
		}
	}
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Aggregate",
		"attributes": map[string]interface{}{
			"groupBy":    groups,
			"aggregates": aggs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalSort
// -----------------------------------------------------------------------

// SortSpec is an ORDER BY expression with direction and null ordering.
type SortSpec struct {
	Expr           ast.Expression
	Ascending      bool
	NullsFirst     bool
	NullsSpecified bool
}

// LogicalSort orders its child.
type LogicalSort struct {
	Child     Plan
	SortSpecs []SortSpec
}

func (n *LogicalSort) Children() []Plan           { return []Plan{n.Child} }
func (n *LogicalSort) Schema() []catalog.Column   { return n.Child.Schema() }

func (n *LogicalSort) String() string {
	specs := make([]string, len(n.SortSpecs))
	for i, s := range n.SortSpecs {
		dir := "ASC"
		if !s.Ascending {
			dir = "DESC"
		}
		specs[i] = fmt.Sprintf("%s %s", ast.PrintExpr(s.Expr), dir)
	}
	return fmt.Sprintf("LogicalSort([%s])", strings.Join(specs, ", "))
}

func (n *LogicalSort) ToJSON() map[string]interface{} {
	specs := make([]string, len(n.SortSpecs))
	for i, s := range n.SortSpecs {
		dir := "ASC"
		if !s.Ascending {
			dir = "DESC"
		}
		specs[i] = fmt.Sprintf("%s %s", ast.PrintExpr(s.Expr), dir)
	}
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Sort",
		"attributes": map[string]interface{}{
			"sortSpecs": specs,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalLimit
// -----------------------------------------------------------------------

// LogicalLimit applies LIMIT/OFFSET.
type LogicalLimit struct {
	Child  Plan
	Count  ast.Expression
	Offset ast.Expression
}

func (n *LogicalLimit) Children() []Plan         { return []Plan{n.Child} }
func (n *LogicalLimit) Schema() []catalog.Column { return n.Child.Schema() }

func (n *LogicalLimit) String() string {
	if n.Offset != nil {
		return fmt.Sprintf("LogicalLimit(count=%s, offset=%s)",
			ast.PrintExpr(n.Count), ast.PrintExpr(n.Offset))
	}
	return fmt.Sprintf("LogicalLimit(count=%s)", ast.PrintExpr(n.Count))
}

func (n *LogicalLimit) ToJSON() map[string]interface{} {
	attrs := map[string]interface{}{
		"count": ast.PrintExpr(n.Count),
	}
	if n.Offset != nil {
		attrs["offset"] = ast.PrintExpr(n.Offset)
	}
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Limit",
		"attributes": attrs,
		"children":   []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalSubquery
// -----------------------------------------------------------------------

// LogicalSubquery wraps a derived table subquery.
type LogicalSubquery struct {
	Child Plan
	Alias string
}

func (n *LogicalSubquery) Children() []Plan         { return []Plan{n.Child} }
func (n *LogicalSubquery) Schema() []catalog.Column { return n.Child.Schema() }

func (n *LogicalSubquery) String() string {
	return fmt.Sprintf("LogicalSubquery(alias=%s)", n.Alias)
}

func (n *LogicalSubquery) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": "Subquery",
		"attributes": map[string]interface{}{
			"alias": n.Alias,
		},
		"children": []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalSetOp
// -----------------------------------------------------------------------

// LogicalSetOp represents UNION / INTERSECT / EXCEPT.
type LogicalSetOp struct {
	Op   string // "UNION", "INTERSECT", "EXCEPT"
	All  bool
	Left Plan
	Right Plan
}

func (n *LogicalSetOp) Children() []Plan { return []Plan{n.Left, n.Right} }

// Schema returns the left side's schema (per SQL standard).
func (n *LogicalSetOp) Schema() []catalog.Column { return n.Left.Schema() }

func (n *LogicalSetOp) String() string {
	all := ""
	if n.All {
		all = " ALL"
	}
	return fmt.Sprintf("LogicalSetOp(%s%s)", n.Op, all)
}

func (n *LogicalSetOp) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":   fmt.Sprintf("node-%d", nextID()),
		"type": n.Op,
		"attributes": map[string]interface{}{
			"all": n.All,
		},
		"children": []interface{}{n.Left.ToJSON(), n.Right.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// -----------------------------------------------------------------------
// LogicalInsert
// -----------------------------------------------------------------------

// LogicalInsert inserts one or more rows into a table.
type LogicalInsert struct {
	TableName string
	Table     *catalog.Table
	Columns   []string
	ValueRows [][]ast.Expression // all rows to insert (nil for INSERT SELECT)
	SelectSrc Plan               // non-nil for INSERT ... SELECT
}

func (n *LogicalInsert) Children() []Plan {
	if n.SelectSrc != nil {
		return []Plan{n.SelectSrc}
	}
	return nil
}
func (n *LogicalInsert) Schema() []catalog.Column {
	return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}}
}
func (n *LogicalInsert) String() string { return fmt.Sprintf("LogicalInsert(%s)", n.TableName) }
func (n *LogicalInsert) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Insert",
		"attributes": map[string]interface{}{"table": n.TableName},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// LogicalUpdate / LogicalDelete
// -----------------------------------------------------------------------

// UpdateAssign mirrors ast.UpdateAssign for the logical plan.
type UpdateAssign struct {
	Column string
	Value  ast.Expression
}

// LogicalUpdate updates rows in a table that match a WHERE predicate.
type LogicalUpdate struct {
	TableName string
	Table     *catalog.Table
	Assigns   []UpdateAssign
	Where     ast.Expression
}

func (n *LogicalUpdate) Children() []Plan         { return nil }
func (n *LogicalUpdate) Schema() []catalog.Column {
	return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}}
}
func (n *LogicalUpdate) String() string { return fmt.Sprintf("LogicalUpdate(%s)", n.TableName) }
func (n *LogicalUpdate) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Update",
		"attributes": map[string]interface{}{"table": n.TableName},
		"children":   []interface{}{},
	}
}

// LogicalDelete deletes rows from a table that match a WHERE predicate.
type LogicalDelete struct {
	TableName string
	Table     *catalog.Table
	Where     ast.Expression
}

func (n *LogicalDelete) Children() []Plan         { return nil }
func (n *LogicalDelete) Schema() []catalog.Column {
	return []catalog.Column{{Name: "rows_affected", Type: catalog.TypeInt, Index: 0}}
}
func (n *LogicalDelete) String() string { return fmt.Sprintf("LogicalDelete(%s)", n.TableName) }
func (n *LogicalDelete) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Delete",
		"attributes": map[string]interface{}{"table": n.TableName},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// LogicalConstant
// -----------------------------------------------------------------------

// LogicalConstant produces exactly one row with no columns.
// Used as the implicit FROM source for SELECT without a FROM clause (e.g. SELECT 1+1).
type LogicalConstant struct{}

func (n *LogicalConstant) Children() []Plan         { return nil }
func (n *LogicalConstant) Schema() []catalog.Column { return nil }
func (n *LogicalConstant) String() string           { return "Constant" }
func (n *LogicalConstant) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Constant",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// LogicalDistinct
// -----------------------------------------------------------------------

// LogicalDistinct deduplicates its child's output (implements SELECT DISTINCT).
type LogicalDistinct struct {
	Child Plan
}

func (n *LogicalDistinct) Children() []Plan         { return []Plan{n.Child} }
func (n *LogicalDistinct) Schema() []catalog.Column { return n.Child.Schema() }
func (n *LogicalDistinct) String() string           { return "Distinct" }
func (n *LogicalDistinct) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "Distinct",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{n.Child.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// EmptyRelation
// -----------------------------------------------------------------------

// EmptyRelation produces zero rows (used by optimizer for WHERE false).
type EmptyRelation struct {
	Cols []catalog.Column
}

func (n *EmptyRelation) Children() []Plan         { return nil }
func (n *EmptyRelation) Schema() []catalog.Column { return n.Cols }
func (n *EmptyRelation) String() string           { return "EmptyRelation" }
func (n *EmptyRelation) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id":         fmt.Sprintf("node-%d", nextID()),
		"type":       "EmptyRelation",
		"attributes": map[string]interface{}{},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// LogicalExplain
// -----------------------------------------------------------------------

// LogicalExplain wraps a plan for EXPLAIN [ANALYZE].
type LogicalExplain struct {
	Inner   Plan
	Analyze bool
}

func (n *LogicalExplain) Children() []Plan { return []Plan{n.Inner} }
func (n *LogicalExplain) Schema() []catalog.Column {
	return []catalog.Column{{Name: "Plan", Type: catalog.TypeText, Index: 0}}
}
func (n *LogicalExplain) String() string {
	if n.Analyze {
		return "Explain(ANALYZE)"
	}
	return "Explain"
}
func (n *LogicalExplain) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": fmt.Sprintf("node-%d", nextID()), "type": "Explain",
		"attributes": map[string]interface{}{"analyze": n.Analyze},
		"children":   []interface{}{n.Inner.ToJSON()},
	}
}

// -----------------------------------------------------------------------
// LogicalWindow
// -----------------------------------------------------------------------

// WindowExpr pairs a window function expression with its output alias.
type WindowExpr struct {
	Expr  *ast.WindowFuncExpr
	Alias string
}

// LogicalWindow computes window functions over its child.
type LogicalWindow struct {
	Child   Plan
	Windows []WindowExpr
}

func (n *LogicalWindow) Children() []Plan { return []Plan{n.Child} }

func (n *LogicalWindow) Schema() []catalog.Column {
	base := n.Child.Schema()
	result := make([]catalog.Column, len(base)+len(n.Windows))
	copy(result, base)
	for i, w := range n.Windows {
		name := w.Alias
		if name == "" {
			name = strings.ToLower(w.Expr.Func.Name)
		}
		result[len(base)+i] = catalog.Column{Name: name, Type: windowFuncType(w.Expr.Func.Name), Index: len(base) + i}
	}
	return result
}

func (n *LogicalWindow) String() string {
	parts := make([]string, len(n.Windows))
	for i, w := range n.Windows {
		parts[i] = w.Expr.Func.Name + "() OVER (...)"
		if w.Alias != "" {
			parts[i] += " AS " + w.Alias
		}
	}
	return fmt.Sprintf("LogicalWindow([%s])", strings.Join(parts, ", "))
}

func (n *LogicalWindow) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": fmt.Sprintf("node-%d", nextID()), "type": "Window",
		"attributes": map[string]interface{}{}, "children": []interface{}{n.Child.ToJSON()},
	}
}

func windowFuncType(name string) catalog.DataType {
	switch strings.ToUpper(name) {
	case "ROW_NUMBER", "RANK", "DENSE_RANK", "NTILE":
		return catalog.TypeInt
	case "LAG", "LEAD", "FIRST_VALUE", "LAST_VALUE", "NTH_VALUE":
		return catalog.TypeNull // depends on argument type
	default:
		return catalog.TypeFloat
	}
}

// -----------------------------------------------------------------------
// LogicalCreateTable / LogicalDropTable / LogicalAlterTable
// -----------------------------------------------------------------------

// LogicalCreateTable represents CREATE TABLE [AS SELECT].
type LogicalCreateTable struct {
	TableName string
	Columns   []*ast.ColumnDef
	SelectSrc Plan // non-nil for CREATE TABLE ... AS SELECT
}

func (n *LogicalCreateTable) Children() []Plan {
	if n.SelectSrc != nil {
		return []Plan{n.SelectSrc}
	}
	return nil
}
func (n *LogicalCreateTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *LogicalCreateTable) String() string { return fmt.Sprintf("LogicalCreateTable(%s)", n.TableName) }
func (n *LogicalCreateTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": fmt.Sprintf("node-%d", nextID()), "type": "CreateTable",
		"attributes": map[string]interface{}{"table": n.TableName}, "children": []interface{}{},
	}
}

// LogicalDropTable represents DROP TABLE [IF EXISTS].
type LogicalDropTable struct {
	TableName string
	IfExists  bool
}

func (n *LogicalDropTable) Children() []Plan         { return nil }
func (n *LogicalDropTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *LogicalDropTable) String() string { return fmt.Sprintf("LogicalDropTable(%s)", n.TableName) }
func (n *LogicalDropTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": fmt.Sprintf("node-%d", nextID()), "type": "DropTable",
		"attributes": map[string]interface{}{"table": n.TableName}, "children": []interface{}{},
	}
}

// LogicalAlterTable represents ALTER TABLE.
type LogicalAlterTable struct {
	TableName  string
	Action     string          // "ADD", "DROP", "RENAME", "RENAME_COLUMN"
	ColDef     *catalog.Column // for ADD (converted from ast.ColumnDef)
	ColName    string          // for DROP / RENAME_COLUMN
	NewName    string          // for RENAME / RENAME_COLUMN
	DefaultVal ast.Expression  // for ADD with DEFAULT
}

func (n *LogicalAlterTable) Children() []Plan         { return nil }
func (n *LogicalAlterTable) Schema() []catalog.Column {
	return []catalog.Column{{Name: "result", Type: catalog.TypeText, Index: 0}}
}
func (n *LogicalAlterTable) String() string {
	return fmt.Sprintf("LogicalAlterTable(%s %s)", n.TableName, n.Action)
}
func (n *LogicalAlterTable) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": fmt.Sprintf("node-%d", nextID()), "type": "AlterTable",
		"attributes": map[string]interface{}{"table": n.TableName, "action": n.Action},
		"children":   []interface{}{},
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func inferExprType(expr ast.Expression, schema []catalog.Column) catalog.DataType {
	switch e := expr.(type) {
	case *ast.IntLiteral:
		return catalog.TypeInt
	case *ast.FloatLiteral:
		return catalog.TypeFloat
	case *ast.StringLiteral:
		return catalog.TypeText
	case *ast.BoolLiteral:
		return catalog.TypeBool
	case *ast.NullLiteral:
		return catalog.TypeNull
	case *ast.ColumnRef:
		name := e.ResolvedTable + "." + e.Column
		for _, col := range schema {
			if col.Name == name {
				return col.Type
			}
		}
		// Bare column fallback
		for _, col := range schema {
			if strings.HasSuffix(col.Name, "."+e.Column) || col.Name == e.Column {
				return col.Type
			}
		}
		return catalog.TypeNull
	case *ast.FunctionCall:
		return aggResultType(e.Name)
	case *ast.WindowFuncExpr:
		return windowFuncType(e.Func.Name)
	case *ast.ExtractExpr:
		return catalog.TypeFloat
	case *ast.AliasExpr:
		return inferExprType(e.Expr, schema)
	case *ast.BinaryExpr:
		lt := inferExprType(e.Left, schema)
		rt := inferExprType(e.Right, schema)
		if lt == catalog.TypeFloat || rt == catalog.TypeFloat {
			return catalog.TypeFloat
		}
		return lt
	default:
		return catalog.TypeNull
	}
}

func aggResultType(fn string) catalog.DataType {
	switch strings.ToUpper(fn) {
	case "COUNT":
		return catalog.TypeInt
	case "SUM", "AVG", "STDDEV", "STDDEV_POP", "STDDEV_SAMP", "VAR_POP", "VAR_SAMP", "VARIANCE":
		return catalog.TypeFloat
	case "MIN", "MAX":
		return catalog.TypeText // conservative fallback
	case "ROW_NUMBER", "RANK", "DENSE_RANK", "NTILE":
		return catalog.TypeInt
	default:
		return catalog.TypeNull
	}
}
