package logical

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/lexer"
)

// Builder converts an analyzed AST into a logical plan.
type Builder struct {
	catalog     *catalog.Catalog
	ctes        map[string]*ast.SelectStatement // active CTE definitions (name → query)
	ctePlans    map[string]Plan                 // pre-built plans for set-op CTE bodies
	buildingCTE map[string]bool                 // cycle guard: CTEs currently being expanded
}

// NewBuilder creates a new logical plan builder.
func NewBuilder(cat *catalog.Catalog) *Builder {
	return &Builder{catalog: cat}
}

// InjectCTEs seeds the builder with pre-existing CTE definitions (e.g. from an
// outer query's WITH clause) so that subquery builders can resolve CTE names.
func (b *Builder) InjectCTEs(ctes map[string]*ast.SelectStatement) {
	if len(ctes) == 0 {
		return
	}
	if b.ctes == nil {
		b.ctes = make(map[string]*ast.SelectStatement, len(ctes))
	}
	for k, v := range ctes {
		b.ctes[k] = v
	}
}

// GetCTEs returns the current CTE definitions registered in this builder so
// they can be propagated to runtime subquery runners.
func (b *Builder) GetCTEs() map[string]*ast.SelectStatement {
	return b.ctes
}

// Build converts a SelectStatement into a logical plan.
func (b *Builder) Build(stmt *ast.SelectStatement) (Plan, error) {
	return b.buildSelect(stmt)
}

// BuildStatement converts any supported statement into a logical plan.
func (b *Builder) BuildStatement(stmt ast.Statement) (Plan, error) {
	switch s := stmt.(type) {
	case *ast.SelectStatement:
		return b.buildSelect(s)
	case *ast.SetOpStatement:
		return b.buildSetOp(s)
	case *ast.InsertStatement:
		return b.buildInsert(s)
	case *ast.UpdateStatement:
		return b.buildUpdate(s)
	case *ast.DeleteStatement:
		return b.buildDelete(s)
	case *ast.ExplainStatement:
		// EXPLAIN wraps the inner statement — the executor handles it specially
		inner, err := b.BuildStatement(s.Stmt)
		if err != nil {
			return nil, err
		}
		return &LogicalExplain{Inner: inner, Analyze: s.Analyze}, nil
	case *ast.CreateTableStatement:
		return b.buildCreateTable(s)
	case *ast.DropTableStatement:
		return &LogicalDropTable{TableName: s.Name, IfExists: s.IfExists}, nil
	case *ast.AlterTableStatement:
		return b.buildAlterTable(s)
	default:
		return nil, fmt.Errorf("logical planner: unsupported statement type %T", stmt)
	}
}

// buildCreateTable builds a LogicalCreateTable plan.
func (b *Builder) buildCreateTable(s *ast.CreateTableStatement) (Plan, error) {
	node := &LogicalCreateTable{
		TableName: s.Name,
		Columns:   s.Columns,
	}
	if s.SelectStmt != nil {
		src, err := b.buildSelect(s.SelectStmt)
		if err != nil {
			return nil, fmt.Errorf("CREATE TABLE AS SELECT: %w", err)
		}
		node.SelectSrc = src
	}
	return node, nil
}

// buildAlterTable builds a LogicalAlterTable plan.
func (b *Builder) buildAlterTable(s *ast.AlterTableStatement) (Plan, error) {
	node := &LogicalAlterTable{
		TableName: s.Table,
		Action:    s.Action,
		ColName:   s.ColName,
		NewName:   s.NewName,
	}
	if s.ColDef != nil {
		dt, err := catalog.ParseDataType(s.ColDef.TypeName)
		if err != nil {
			return nil, fmt.Errorf("ALTER TABLE: %w", err)
		}
		col := &catalog.Column{
			Name:     s.ColDef.Name,
			Type:     dt,
			Nullable: !s.ColDef.NotNull,
			PK:       s.ColDef.PrimaryKey,
		}
		node.ColDef = col
		node.DefaultVal = s.ColDef.DefaultVal
	}
	return node, nil
}

// buildInsert builds a LogicalInsert plan from an INSERT statement.
func (b *Builder) buildInsert(s *ast.InsertStatement) (Plan, error) {
	table, ok := b.catalog.Lookup(s.Table)
	if !ok {
		return nil, fmt.Errorf("INSERT: table %q not found", s.Table)
	}
	if s.SelectStmt != nil {
		// INSERT ... SELECT
		src, err := b.buildSelect(s.SelectStmt)
		if err != nil {
			return nil, fmt.Errorf("INSERT SELECT: %w", err)
		}
		return &LogicalInsert{
			TableName: s.Table,
			Table:     table,
			Columns:   s.Columns,
			SelectSrc: src,
		}, nil
	}
	rows := s.ValueRows
	if len(rows) == 0 {
		rows = [][]ast.Expression{s.Values}
	}
	return &LogicalInsert{
		TableName: s.Table,
		Table:     table,
		Columns:   s.Columns,
		ValueRows: rows,
	}, nil
}

// buildUpdate builds a LogicalUpdate plan from an UPDATE statement.
func (b *Builder) buildUpdate(s *ast.UpdateStatement) (Plan, error) {
	table, ok := b.catalog.Lookup(s.Table)
	if !ok {
		return nil, fmt.Errorf("UPDATE: table %q not found", s.Table)
	}
	assigns := make([]UpdateAssign, len(s.Assigns))
	for i, a := range s.Assigns {
		assigns[i] = UpdateAssign{Column: a.Column, Value: a.Value}
	}
	return &LogicalUpdate{
		TableName: s.Table,
		Table:     table,
		Assigns:   assigns,
		Where:     s.Where,
	}, nil
}

// buildDelete builds a LogicalDelete plan from a DELETE statement.
func (b *Builder) buildDelete(s *ast.DeleteStatement) (Plan, error) {
	table, ok := b.catalog.Lookup(s.Table)
	if !ok {
		return nil, fmt.Errorf("DELETE: table %q not found", s.Table)
	}
	return &LogicalDelete{
		TableName: s.Table,
		Table:     table,
		Where:     s.Where,
	}, nil
}

// buildSetOp builds a LogicalSetOp from a SetOpStatement.
func (b *Builder) buildSetOp(s *ast.SetOpStatement) (Plan, error) {
	left, err := b.BuildStatement(s.Left)
	if err != nil {
		return nil, err
	}
	right, err := b.buildSelect(s.Right)
	if err != nil {
		return nil, err
	}
	return &LogicalSetOp{Op: s.Op, All: s.All, Left: left, Right: right}, nil
}

func (b *Builder) buildSelect(sel *ast.SelectStatement) (Plan, error) {
	// Push CTE definitions into scope for the duration of this SELECT.
	if len(sel.CTEs) > 0 {
		if b.ctes == nil {
			b.ctes = make(map[string]*ast.SelectStatement)
		}
		for _, cte := range sel.CTEs {
			name := strings.ToLower(cte.Name)
			if cte.BodyStmt != nil {
				// Non-recursive CTE with a set-op body (UNION/INTERSECT/EXCEPT):
				// build the plan immediately and cache it.
				plan, err := b.BuildStatement(cte.BodyStmt)
				if err != nil {
					return nil, fmt.Errorf("CTE %q: %w", cte.Name, err)
				}
				if b.ctePlans == nil {
					b.ctePlans = make(map[string]Plan)
				}
				b.ctePlans[name] = plan
			} else if cte.Recursive && cte.RecursiveSelect != nil {
				return nil, fmt.Errorf("CTE %q: recursive CTEs (WITH RECURSIVE) are not yet implemented", cte.Name)
			} else {
				b.ctes[name] = cte.Select
			}
		}
	}

	// FROM clause. When there is no FROM (e.g. SELECT 1+1 or SELECT 1 WHERE 1=1),
	// use a synthetic single-row constant relation so that downstream operators
	// always have a non-nil child.
	var plan Plan
	if sel.From != nil {
		var err error
		plan, err = b.buildTableRef(sel.From)
		if err != nil {
			return nil, err
		}
	} else {
		plan = &LogicalConstant{}
	}

	// JOINs
	for _, j := range sel.Joins {
		rightPlan, err := b.buildTableRef(j.Table)
		if err != nil {
			return nil, err
		}
		jt := astJoinType(j.JoinType)
		cond := j.Condition

		// NATURAL JOIN: auto-derive condition from common column names
		if j.Natural || len(j.Using) > 0 {
			leftSchema := plan.Schema()
			rightSchema := rightPlan.Schema()
			cols := j.Using // explicit USING list
			if j.Natural {
				// Find all common short column names
				cols = nil
				for _, lc := range leftSchema {
					lshort := shortColName(lc.Name)
					for _, rc := range rightSchema {
						if strings.EqualFold(lshort, shortColName(rc.Name)) {
							cols = append(cols, lshort)
							break
						}
					}
				}
			}
			cond = buildJoinConditionFromCols(cols, leftSchema, rightSchema)
		}

		plan = &LogicalJoin{
			Left:      plan,
			Right:     rightPlan,
			JoinType:  jt,
			Condition: cond,
		}
	}

	// WHERE
	if sel.Where != nil {
		plan = &LogicalFilter{Child: plan, Predicate: sel.Where}
	}

	// GROUP BY / HAVING
	var aggNode *LogicalAggregate
	if len(sel.GroupBy) > 0 || hasAggregate(sel.Columns) || (sel.Having != nil && exprHasAggregate(sel.Having)) {
		aggs, err := extractAggregates(sel.Columns)
		if err != nil {
			return nil, err
		}
		// Also collect any aggregate functions used only in the HAVING clause
		// (e.g. HAVING COUNT(*) > 5 when COUNT(*) is not in the SELECT list).
		if sel.Having != nil {
			aggs = mergeHavingAggs(aggs, sel.Having)
		}
		aggNode = &LogicalAggregate{
			Child:   plan,
			GroupBy: sel.GroupBy,
			Aggs:    aggs,
		}
		plan = aggNode
		// HAVING becomes a filter above the aggregate.
		// Remap any aggregate calls in the HAVING predicate to column references.
		if sel.Having != nil {
			havingSchema := aggNode.Schema()
			havingSigMap := buildAggSigMap(aggNode, havingSchema)
			remappedHaving := remapExpr(sel.Having, havingSchema, havingSigMap)
			plan = &LogicalFilter{Child: plan, Predicate: remappedHaving}
		}
	}

	// Window functions: detect WindowFuncExpr in the SELECT list,
	// insert a LogicalWindow node, and replace with ColumnRef in projection.
	projCols := sel.Columns
	if aggNode != nil {
		projCols = remapAggExprs(sel.Columns, aggNode)
	}
	windowExprs := extractWindowExprs(projCols)
	if len(windowExprs) > 0 {
		winNode := &LogicalWindow{Child: plan, Windows: windowExprs}
		plan = winNode
		projCols = replaceWindowFuncs(projCols, windowExprs, winNode.Schema())
	}

	// SELECT list (projection)
	exprs, aliases := projectExpressions(projCols)
	plan = &LogicalProject{
		Child:       plan,
		Expressions: exprs,
		Aliases:     aliases,
	}

	// DISTINCT: wrap with LogicalDistinct before ORDER BY / LIMIT so that
	// deduplication happens on the projected (post-GROUP BY) output.
	if sel.Distinct {
		plan = &LogicalDistinct{Child: plan}
	}

	// ORDER BY
	if len(sel.OrderBy) > 0 {
		specs := make([]SortSpec, len(sel.OrderBy))
		for i, s := range sel.OrderBy {
			specs[i] = SortSpec{
				Expr:           s.Expr,
				Ascending:      s.Ascending,
				NullsFirst:     s.NullsFirst,
				NullsSpecified: s.NullsSpecified,
			}
		}
		plan = &LogicalSort{Child: plan, SortSpecs: specs}
	}

	// LIMIT / OFFSET
	if sel.Limit != nil {
		plan = &LogicalLimit{Child: plan, Count: sel.Limit, Offset: sel.Offset}
	}

	return plan, nil
}

func (b *Builder) buildTableRef(ref *ast.TableRef) (Plan, error) {
	if ref.Subquery != nil {
		subPlan, err := b.buildSelect(ref.Subquery)
		if err != nil {
			return nil, err
		}
		return &LogicalSubquery{Child: subPlan, Alias: ref.Alias}, nil
	}

	// Check CTE scope first (pre-built plans for set-op bodies)
	if b.ctePlans != nil {
		if plan, ok := b.ctePlans[strings.ToLower(ref.Name)]; ok {
			alias := ref.Alias
			if alias == "" {
				alias = ref.Name
			}
			return &LogicalSubquery{Child: plan, Alias: alias}, nil
		}
	}
	if b.ctes != nil {
		key := strings.ToLower(ref.Name)
		if cteSel, ok := b.ctes[key]; ok {
			if b.buildingCTE[key] {
				return nil, fmt.Errorf("CTE %q is self-referential; use WITH RECURSIVE for recursive CTEs", ref.Name)
			}
			alias := ref.Alias
			if alias == "" {
				alias = ref.Name
			}
			if b.buildingCTE == nil {
				b.buildingCTE = make(map[string]bool)
			}
			b.buildingCTE[key] = true
			subPlan, err := b.buildSelect(cteSel)
			delete(b.buildingCTE, key)
			if err != nil {
				return nil, err
			}
			return &LogicalSubquery{Child: subPlan, Alias: alias}, nil
		}
	}

	table, ok := b.catalog.Lookup(ref.Name)
	if !ok {
		return nil, fmt.Errorf("table %q not found in catalog", ref.Name)
	}

	alias := ref.Alias
	if alias == "" {
		alias = ref.Name
	}

	return &LogicalScan{TableName: ref.Name, Alias: alias, Table: table}, nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func astJoinType(jt ast.JoinType) JoinType {
	switch jt {
	case ast.JoinLeft:
		return LeftJoin
	case ast.JoinRight:
		return RightJoin
	case ast.JoinCross:
		return CrossJoin
	case ast.JoinFull:
		return FullJoin
	default:
		return InnerJoin
	}
}

func hasAggregate(cols []ast.Expression) bool {
	for _, col := range cols {
		if exprHasAggregate(col) {
			return true
		}
	}
	return false
}

func exprHasAggregate(expr ast.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.FunctionCall:
		return isAggFunc(e.Name)
	case *ast.AliasExpr:
		return exprHasAggregate(e.Expr)
	case *ast.BinaryExpr:
		return exprHasAggregate(e.Left) || exprHasAggregate(e.Right)
	case *ast.UnaryExpr:
		return exprHasAggregate(e.Expr)
	case *ast.CaseExpr:
		if exprHasAggregate(e.Operand) {
			return true
		}
		for _, w := range e.Whens {
			if exprHasAggregate(w.Condition) || exprHasAggregate(w.Result) {
				return true
			}
		}
		return exprHasAggregate(e.ElseExpr)
	case *ast.InExpr:
		if exprHasAggregate(e.Expr) {
			return true
		}
		for _, item := range e.List {
			if exprHasAggregate(item) {
				return true
			}
		}
	case *ast.BetweenExpr:
		return exprHasAggregate(e.Expr) || exprHasAggregate(e.Low) || exprHasAggregate(e.High)
	case *ast.IsNullExpr:
		return exprHasAggregate(e.Expr)
	}
	return false
}

func isAggFunc(name string) bool {
	switch strings.ToUpper(name) {
	case "COUNT", "SUM", "AVG", "MIN", "MAX",
		"VARIANCE", "VAR_POP", "VAR_SAMP",
		"STDDEV", "STDDEV_POP", "STDDEV_SAMP":
		return true
	}
	return false
}

// extractAggregates collects aggregate expressions from the SELECT list.
func extractAggregates(cols []ast.Expression) ([]AggExpr, error) {
	var aggs []AggExpr
	for _, col := range cols {
		collectAgg(col, &aggs)
	}
	return aggs, nil
}

func collectAgg(expr ast.Expression, aggs *[]AggExpr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.FunctionCall:
		if isAggFunc(e.Name) {
			agg := AggExpr{Function: strings.ToUpper(e.Name), StarArg: e.StarArg, Distinct: e.Distinct}
			if !e.StarArg && len(e.Args) > 0 {
				agg.Arg = e.Args[0]
			}
			*aggs = append(*aggs, agg)
		}
	case *ast.AliasExpr:
		if fn, ok := e.Expr.(*ast.FunctionCall); ok && isAggFunc(fn.Name) {
			agg := AggExpr{
				Function: strings.ToUpper(fn.Name),
				StarArg:  fn.StarArg,
				Distinct: fn.Distinct,
				Alias:    e.Alias,
			}
			if !fn.StarArg && len(fn.Args) > 0 {
				agg.Arg = fn.Args[0]
			}
			*aggs = append(*aggs, agg)
			return
		}
		collectAgg(e.Expr, aggs)
	case *ast.BinaryExpr:
		collectAgg(e.Left, aggs)
		collectAgg(e.Right, aggs)
	case *ast.UnaryExpr:
		collectAgg(e.Expr, aggs)
	case *ast.CaseExpr:
		collectAgg(e.Operand, aggs)
		for _, w := range e.Whens {
			collectAgg(w.Condition, aggs)
			collectAgg(w.Result, aggs)
		}
		collectAgg(e.ElseExpr, aggs)
	case *ast.InExpr:
		collectAgg(e.Expr, aggs)
		for _, item := range e.List {
			collectAgg(item, aggs)
		}
	case *ast.BetweenExpr:
		collectAgg(e.Expr, aggs)
		collectAgg(e.Low, aggs)
		collectAgg(e.High, aggs)
	case *ast.IsNullExpr:
		collectAgg(e.Expr, aggs)
	}
}

// aggSig returns a canonical signature string for an AggExpr.
func aggSig(a AggExpr) string {
	if a.StarArg {
		return strings.ToUpper(a.Function) + "(*)"
	}
	if a.Arg != nil {
		return strings.ToUpper(a.Function) + "(" + ast.PrintExpr(a.Arg) + ")"
	}
	return strings.ToUpper(a.Function) + "()"
}

// mergeHavingAggs adds any aggregate functions found in the HAVING predicate
// that are not already present in aggs (e.g. HAVING COUNT(*) > 5 without
// COUNT(*) in the SELECT list).
func mergeHavingAggs(aggs []AggExpr, having ast.Expression) []AggExpr {
	existing := make(map[string]struct{}, len(aggs))
	for _, a := range aggs {
		existing[aggSig(a)] = struct{}{}
	}
	var extra []AggExpr
	collectAgg(having, &extra)
	for _, a := range extra {
		if _, ok := existing[aggSig(a)]; !ok {
			aggs = append(aggs, a)
		}
	}
	return aggs
}

// remapAggExprs replaces aggregate function calls in the SELECT list with
// ColumnRef nodes that reference the aggregate's output schema, so the
// Projection above the aggregate doesn't try to re-evaluate them as scalars.
func remapAggExprs(cols []ast.Expression, agg *LogicalAggregate) []ast.Expression {
	aggSchema := agg.Schema()
	sigMap := buildAggSigMap(agg, aggSchema)
	result := make([]ast.Expression, len(cols))
	for i, col := range cols {
		result[i] = remapExpr(col, aggSchema, sigMap)
	}
	return result
}

// buildAggSigMap builds a map from aggregate function signature (e.g. "COUNT(*)")
// to the output catalog.Column, handling aliased aggregates correctly.
func buildAggSigMap(agg *LogicalAggregate, aggSchema []catalog.Column) map[string]catalog.Column {
	nGroupBy := len(agg.GroupBy)
	sigMap := make(map[string]catalog.Column, len(agg.Aggs))
	for i, a := range agg.Aggs {
		var sig string
		if a.StarArg {
			sig = strings.ToUpper(a.Function) + "(*)"
		} else if a.Arg != nil {
			sig = strings.ToUpper(a.Function) + "(" + ast.PrintExpr(a.Arg) + ")"
		} else {
			sig = strings.ToUpper(a.Function) + "()"
		}
		if idx := nGroupBy + i; idx < len(aggSchema) {
			sigMap[sig] = aggSchema[idx]
		}
	}
	return sigMap
}

func remapExpr(expr ast.Expression, aggSchema []catalog.Column, sigMap map[string]catalog.Column) ast.Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.AliasExpr:
		return &ast.AliasExpr{Pos: e.Pos, Expr: remapExpr(e.Expr, aggSchema, sigMap), Alias: e.Alias}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Op: e.Op, Left: remapExpr(e.Left, aggSchema, sigMap), Right: remapExpr(e.Right, aggSchema, sigMap)}
	case *ast.FunctionCall:
		if !isAggFunc(e.Name) {
			return expr
		}
		// Build the signature for this call and look it up in sigMap first.
		var sig string
		if e.StarArg {
			sig = strings.ToUpper(e.Name) + "(*)"
		} else if len(e.Args) > 0 {
			sig = strings.ToUpper(e.Name) + "(" + ast.PrintExpr(e.Args[0]) + ")"
		} else {
			sig = strings.ToUpper(e.Name) + "()"
		}
		if col, ok := sigMap[sig]; ok {
			return &ast.ColumnRef{Column: col.Name, ResolvedIndex: col.Index}
		}
		// Fallback: match by column name in aggSchema (for unaliased aggs).
		for _, col := range aggSchema {
			if strings.EqualFold(col.Name, sig) {
				return &ast.ColumnRef{Column: col.Name, ResolvedIndex: col.Index}
			}
		}
		return expr
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Op: e.Op, Expr: remapExpr(e.Expr, aggSchema, sigMap)}
	case *ast.CaseExpr:
		newWhens := make([]ast.WhenClause, len(e.Whens))
		for i, w := range e.Whens {
			newWhens[i] = ast.WhenClause{
				Condition: remapExpr(w.Condition, aggSchema, sigMap),
				Result:    remapExpr(w.Result, aggSchema, sigMap),
			}
		}
		return &ast.CaseExpr{
			Pos:      e.Pos,
			Operand:  remapExpr(e.Operand, aggSchema, sigMap),
			Whens:    newWhens,
			ElseExpr: remapExpr(e.ElseExpr, aggSchema, sigMap),
		}
	case *ast.InExpr:
		newList := make([]ast.Expression, len(e.List))
		for i, item := range e.List {
			newList[i] = remapExpr(item, aggSchema, sigMap)
		}
		return &ast.InExpr{Pos: e.Pos, Expr: remapExpr(e.Expr, aggSchema, sigMap), List: newList, Negated: e.Negated}
	case *ast.IsNullExpr:
		return &ast.IsNullExpr{Pos: e.Pos, Expr: remapExpr(e.Expr, aggSchema, sigMap), Negated: e.Negated}
	default:
		return expr
	}
}

// projectExpressions extracts expressions and aliases from the SELECT list.
func projectExpressions(cols []ast.Expression) ([]ast.Expression, []string) {
	exprs := make([]ast.Expression, len(cols))
	aliases := make([]string, len(cols))
	for i, col := range cols {
		switch e := col.(type) {
		case *ast.AliasExpr:
			exprs[i] = e.Expr
			aliases[i] = e.Alias
		default:
			exprs[i] = col
			aliases[i] = ""
		}
	}
	return exprs, aliases
}


// -----------------------------------------------------------------------
// Window function helpers
// -----------------------------------------------------------------------

// extractWindowExprs scans the SELECT list for WindowFuncExpr nodes.
func extractWindowExprs(cols []ast.Expression) []WindowExpr {
	var result []WindowExpr
	idx := 0
	for _, col := range cols {
		inner := col
		alias := ""
		if ae, ok := col.(*ast.AliasExpr); ok {
			inner = ae.Expr
			alias = ae.Alias
		}
		if wf, ok := inner.(*ast.WindowFuncExpr); ok {
			if alias == "" {
				alias = fmt.Sprintf("__win_%d", idx)
			}
			result = append(result, WindowExpr{Expr: wf, Alias: alias})
			idx++
		}
	}
	return result
}

// replaceWindowFuncs replaces WindowFuncExpr nodes in the SELECT list with
// ColumnRef nodes pointing to the Window operator's output columns.
func replaceWindowFuncs(cols []ast.Expression, wins []WindowExpr, schema []catalog.Column) []ast.Expression {
	// Build a lookup: alias → schema column
	aliasToCol := make(map[string]catalog.Column, len(wins))
	for _, w := range wins {
		for _, sc := range schema {
			if sc.Name == w.Alias {
				aliasToCol[w.Alias] = sc
				break
			}
		}
	}

	result := make([]ast.Expression, len(cols))
	for i, col := range cols {
		alias := ""
		inner := col
		if ae, ok := col.(*ast.AliasExpr); ok {
			inner = ae.Expr
			alias = ae.Alias
		}
		if wf, ok := inner.(*ast.WindowFuncExpr); ok {
			// Find the matching win alias
			for _, w := range wins {
				if w.Expr == wf {
					colRef := &ast.ColumnRef{Column: w.Alias}
					if sc, ok2 := aliasToCol[w.Alias]; ok2 {
						colRef.ResolvedIndex = sc.Index
					}
					outAlias := alias
					if outAlias == "" {
						outAlias = w.Alias
					}
					result[i] = &ast.AliasExpr{Expr: colRef, Alias: outAlias}
					break
				}
			}
			if result[i] == nil {
				result[i] = col // fallback
			}
		} else {
			result[i] = col
		}
	}
	return result
}

// -----------------------------------------------------------------------
// NATURAL JOIN / JOIN USING helpers
// -----------------------------------------------------------------------

func shortColName(qualified string) string {
	if dot := strings.LastIndex(qualified, "."); dot >= 0 {
		return qualified[dot+1:]
	}
	return qualified
}

// buildJoinConditionFromCols builds an equality condition for NATURAL JOIN / USING.
func buildJoinConditionFromCols(colNames []string, leftSchema, rightSchema []catalog.Column) ast.Expression {
	var conditions []ast.Expression
	for _, name := range colNames {
		var leftFull, rightFull string
		for _, lc := range leftSchema {
			if strings.EqualFold(shortColName(lc.Name), name) {
				leftFull = lc.Name
				break
			}
		}
		for _, rc := range rightSchema {
			if strings.EqualFold(shortColName(rc.Name), name) {
				rightFull = rc.Name
				break
			}
		}
		if leftFull == "" || rightFull == "" {
			continue
		}
		leftParts := strings.SplitN(leftFull, ".", 2)
		rightParts := strings.SplitN(rightFull, ".", 2)
		var leftTable, leftCol, rightTable, rightCol string
		if len(leftParts) == 2 {
			leftTable, leftCol = leftParts[0], leftParts[1]
		} else {
			leftCol = leftParts[0]
		}
		if len(rightParts) == 2 {
			rightTable, rightCol = rightParts[0], rightParts[1]
		} else {
			rightCol = rightParts[0]
		}
		leftRef := &ast.ColumnRef{Table: leftTable, Column: leftCol}
		rightRef := &ast.ColumnRef{Table: rightTable, Column: rightCol}
		eq := &ast.BinaryExpr{
			Op:    lexer.Token{Type: lexer.EQ, Literal: "="},
			Left:  leftRef,
			Right: rightRef,
		}
		conditions = append(conditions, eq)
	}
	if len(conditions) == 0 {
		return nil
	}
	cond := conditions[0]
	for _, c := range conditions[1:] {
		cond = &ast.BinaryExpr{
			Op:    lexer.Token{Type: lexer.AND, Literal: "AND"},
			Left:  cond,
			Right: c,
		}
	}
	return cond
}
