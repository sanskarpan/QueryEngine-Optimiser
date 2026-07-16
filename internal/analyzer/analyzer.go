// Package analyzer performs semantic analysis on AST nodes produced by the parser.
// It resolves column references, validates table and column names against the catalog,
// checks type compatibility, and rejects unsupported SQL constructs before planning.
package analyzer

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/lexer"
)

// AnalysisError is returned when semantic analysis fails.
type AnalysisError struct {
	Message string
	Line    int
	Col     int
}

func (e *AnalysisError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("analysis error at line %d, col %d: %s", e.Line, e.Col, e.Message)
	}
	return fmt.Sprintf("analysis error: %s", e.Message)
}

func analysisErrorf(pos ast.Pos, format string, args ...interface{}) *AnalysisError {
	return &AnalysisError{
		Message: fmt.Sprintf(format, args...),
		Line:    pos.Line,
		Col:     pos.Col,
	}
}

// Scope tracks the tables available in the current query context.
type Scope struct {
	tables  map[string]*catalog.Table // alias (or table name) → table
	aliases map[string]string         // alias → real table name
}

func newScope() *Scope {
	return &Scope{
		tables:  make(map[string]*catalog.Table),
		aliases: make(map[string]string),
	}
}

// add registers a table under a given alias (or table name if no alias).
func (s *Scope) add(alias, realName string, table *catalog.Table) {
	key := strings.ToLower(alias)
	s.tables[key] = table
	s.aliases[key] = strings.ToLower(realName)
}

// resolveColumn resolves a column reference within this scope.
// Returns (table alias used, *Column, error).
func (s *Scope) resolveColumn(colName, tableAlias string) (string, *catalog.Column, error) {
	colLower := strings.ToLower(colName)

	if tableAlias != "" {
		aliasLower := strings.ToLower(tableAlias)
		table, ok := s.tables[aliasLower]
		if !ok {
			return "", nil, fmt.Errorf("unknown table alias %q", tableAlias)
		}
		col := table.FindColumn(colLower)
		if col == nil {
			return "", nil, fmt.Errorf("column %q not found in table %q", colName, tableAlias)
		}
		return aliasLower, col, nil
	}

	// Unqualified — search all tables in scope
	var found []struct {
		alias string
		col   *catalog.Column
	}
	for alias, table := range s.tables {
		if col := table.FindColumn(colLower); col != nil {
			found = append(found, struct {
				alias string
				col   *catalog.Column
			}{alias, col})
		}
	}
	if len(found) == 0 {
		return "", nil, fmt.Errorf("column %q not found in any table in scope", colName)
	}
	if len(found) > 1 {
		return "", nil, fmt.Errorf("column %q is ambiguous (appears in multiple tables)", colName)
	}
	return found[0].alias, found[0].col, nil
}

// Analyzer performs semantic analysis on parsed SQL statements.
type Analyzer struct {
	catalog     *catalog.Catalog
	ctes        map[string]*ast.SelectStatement // active CTE definitions
	resolvingCTE map[string]bool               // cycle guard for CTE expansion
}

// New creates a new Analyzer backed by the given catalog.
func New(cat *catalog.Catalog) *Analyzer {
	return &Analyzer{catalog: cat}
}

// Analyze validates and annotates a parsed statement.
func (a *Analyzer) Analyze(stmt ast.Statement) error {
	switch s := stmt.(type) {
	case *ast.SelectStatement:
		_, err := a.analyzeSelect(s, nil)
		return err
	case *ast.SetOpStatement:
		return a.analyzeSetOp(s)
	case *ast.CreateTableStatement:
		return a.analyzeCreateTable(s)
	case *ast.InsertStatement:
		return a.analyzeInsert(s)
	case *ast.UpdateStatement:
		return a.analyzeUpdate(s)
	case *ast.DeleteStatement:
		return a.analyzeDelete(s)
	case *ast.ExplainStatement:
		return a.Analyze(s.Stmt)
	case *ast.DropTableStatement:
		if !s.IfExists {
			if _, ok := a.catalog.Lookup(s.Name); !ok {
				return fmt.Errorf("table %q does not exist", s.Name)
			}
		}
		return nil
	case *ast.AlterTableStatement:
		if _, ok := a.catalog.Lookup(s.Table); !ok {
			return fmt.Errorf("table %q does not exist", s.Table)
		}
		return nil
	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

// analyzeSetOp validates both sides of a UNION/INTERSECT/EXCEPT.
func (a *Analyzer) analyzeSetOp(s *ast.SetOpStatement) error {
	if err := a.Analyze(s.Left); err != nil {
		return err
	}
	if _, err := a.analyzeSelect(s.Right, nil); err != nil {
		return err
	}
	return nil
}

// analyzeSelect validates a SELECT and returns the scope produced by its FROM clause.
// outerScope is non-nil for correlated subqueries.
func (a *Analyzer) analyzeSelect(sel *ast.SelectStatement, outerScope *Scope) (*Scope, error) {
	scope := newScope()

	// Register CTE definitions before resolving FROM
	if len(sel.CTEs) > 0 {
		if a.ctes == nil {
			a.ctes = make(map[string]*ast.SelectStatement)
		}
		for _, cte := range sel.CTEs {
			name := strings.ToLower(cte.Name)
			if cte.BodyStmt != nil {
				// Non-recursive CTE with a set-op body: use the left side for schema.
				if setop, ok := cte.BodyStmt.(*ast.SetOpStatement); ok {
					if leftSel, ok := setop.Left.(*ast.SelectStatement); ok {
						a.ctes[name] = leftSel
					}
				}
			} else {
				a.ctes[name] = cte.Select
			}
		}
	}

	// Resolve FROM clause
	if sel.From != nil {
		if err := a.resolveTableRef(sel.From, scope); err != nil {
			return nil, err
		}
	}

	// Resolve JOINs
	for _, j := range sel.Joins {
		if err := a.resolveTableRef(j.Table, scope); err != nil {
			return nil, err
		}
	}

	// Expand SELECT * if present
	if err := a.expandStar(sel, scope); err != nil {
		return nil, err
	}

	// Resolve JOIN ON conditions
	for _, j := range sel.Joins {
		if j.Condition != nil {
			if err := a.resolveExpr(j.Condition, scope, outerScope, false); err != nil {
				return nil, err
			}
		}
	}

	// Resolve WHERE (no aggregates or window functions allowed)
	if sel.Where != nil {
		if err := a.resolveExpr(sel.Where, scope, outerScope, false); err != nil {
			return nil, err
		}
		if containsAggregate(sel.Where) {
			return nil, &AnalysisError{Message: "aggregate functions are not allowed in WHERE clause"}
		}
		if containsWindowFunc(sel.Where) {
			return nil, &AnalysisError{Message: "window functions are not allowed in WHERE clause"}
		}
	}

	// Resolve GROUP BY
	for _, g := range sel.GroupBy {
		if err := a.resolveExpr(g, scope, outerScope, false); err != nil {
			return nil, err
		}
	}

	// Resolve HAVING (aggregates allowed)
	if sel.Having != nil {
		if err := a.resolveExpr(sel.Having, scope, outerScope, true); err != nil {
			return nil, err
		}
	}

	// Resolve SELECT column expressions
	hasAgg := false
	hasNonAgg := false
	for _, col := range sel.Columns {
		if err := a.resolveExpr(col, scope, outerScope, true); err != nil {
			return nil, err
		}
		if containsAggregate(col) {
			hasAgg = true
		} else if !isStarExpr(col) {
			hasNonAgg = true
		}
	}

	// If there's an aggregate without GROUP BY and also non-aggregate columns, it's an error
	if hasAgg && hasNonAgg && len(sel.GroupBy) == 0 {
		return nil, &AnalysisError{
			Message: "SELECT list mixes aggregate and non-aggregate expressions without GROUP BY",
		}
	}

	// When GROUP BY is present, every non-aggregate column in SELECT must appear in GROUP BY.
	if hasAgg && len(sel.GroupBy) > 0 {
		groupByKeys := make(map[string]struct{})
		for _, g := range sel.GroupBy {
			expr := g
			if ae, ok := expr.(*ast.AliasExpr); ok {
				expr = ae.Expr
			}
			if cr, ok := expr.(*ast.ColumnRef); ok {
				groupByKeys[strings.ToLower(cr.Column)] = struct{}{}
				if cr.ResolvedTable != "" {
					groupByKeys[strings.ToLower(cr.ResolvedTable+"."+cr.Column)] = struct{}{}
				}
			}
		}
		for _, col := range sel.Columns {
			if isStarExpr(col) || containsAggregate(col) {
				continue
			}
			expr := col
			if ae, ok := expr.(*ast.AliasExpr); ok {
				expr = ae.Expr
			}
			if cr, ok := expr.(*ast.ColumnRef); ok {
				colLow := strings.ToLower(cr.Column)
				qualLow := strings.ToLower(cr.ResolvedTable + "." + cr.Column)
				if _, ok := groupByKeys[colLow]; !ok {
					if _, ok := groupByKeys[qualLow]; !ok {
						return nil, &AnalysisError{
							Message: fmt.Sprintf("column %q must appear in the GROUP BY clause or be used in an aggregate function", cr.Column),
						}
					}
				}
			}
		}
	}

	// Collect SELECT list aliases for ORDER BY resolution
	selectAliases := make(map[string]struct{})
	for _, col := range sel.Columns {
		if alias, ok := col.(*ast.AliasExpr); ok && alias.Alias != "" {
			selectAliases[strings.ToLower(alias.Alias)] = struct{}{}
		}
	}

	// Resolve ORDER BY; bare column refs that match a SELECT alias are valid
	for _, s := range sel.OrderBy {
		if cr, ok := s.Expr.(*ast.ColumnRef); ok && cr.Table == "" {
			if _, isAlias := selectAliases[strings.ToLower(cr.Column)]; isAlias {
				continue // alias reference is fine
			}
		}
		if err := a.resolveExpr(s.Expr, scope, outerScope, true); err != nil {
			return nil, err
		}
	}

	// Resolve LIMIT/OFFSET
	if sel.Limit != nil {
		if err := a.resolveExpr(sel.Limit, scope, outerScope, false); err != nil {
			return nil, err
		}
	}
	if sel.Offset != nil {
		if err := a.resolveExpr(sel.Offset, scope, outerScope, false); err != nil {
			return nil, err
		}
	}

	return scope, nil
}

func (a *Analyzer) resolveTableRef(ref *ast.TableRef, scope *Scope) error {
	if ref.Subquery != nil {
		// Subquery in FROM — analyze the subquery independently
		_, err := a.analyzeSelect(ref.Subquery, nil)
		if err != nil {
			return err
		}
		// The subquery produces an anonymous table — we can't fully validate column references
		// against it without running it. We add a placeholder.
		// For now, create a synthetic table with no columns (skips column resolution for derived tables).
		synth := &catalog.Table{Name: ref.Alias}
		scope.add(ref.Alias, ref.Alias, synth)
		return nil
	}

	// Check CTE scope first
	if a.ctes != nil {
		key := strings.ToLower(ref.Name)
		if cteSel, ok := a.ctes[key]; ok {
			if a.resolvingCTE[key] {
				return analysisErrorf(ref.Pos, "CTE %q is self-referential; use WITH RECURSIVE for recursive CTEs", ref.Name)
			}
			if a.resolvingCTE == nil {
				a.resolvingCTE = make(map[string]bool)
			}
			a.resolvingCTE[key] = true
			_, err := a.analyzeSelect(cteSel, nil)
			delete(a.resolvingCTE, key)
			if err != nil {
				return err
			}
			alias := ref.Alias
			if alias == "" {
				alias = ref.Name
			}
			synth := &catalog.Table{Name: alias, Columns: extractSelectColumns(cteSel)}
			scope.add(alias, alias, synth)
			return nil
		}
	}

	table, ok := a.catalog.Lookup(ref.Name)
	if !ok {
		return analysisErrorf(ref.Pos, "table %q not found in catalog", ref.Name)
	}

	alias := ref.Alias
	if alias == "" {
		alias = ref.Name
	}
	scope.add(alias, ref.Name, table)
	return nil
}

func (a *Analyzer) expandStar(sel *ast.SelectStatement, scope *Scope) error {
	// Check if any column is a StarExpr
	hasStar := false
	for _, col := range sel.Columns {
		if _, ok := col.(*ast.StarExpr); ok {
			hasStar = true
			break
		}
	}
	if !hasStar {
		return nil
	}

	// Build explicit column list from scope
	// Maintain table order from FROM/JOIN clauses
	var expanded []ast.Expression

	// Collect aliases in order (FROM first, then JOINs)
	var orderedAliases []string
	if sel.From != nil {
		alias := sel.From.Alias
		if alias == "" {
			alias = sel.From.Name
		}
		orderedAliases = append(orderedAliases, strings.ToLower(alias))
	}
	for _, j := range sel.Joins {
		alias := j.Table.Alias
		if alias == "" {
			alias = j.Table.Name
		}
		orderedAliases = append(orderedAliases, strings.ToLower(alias))
	}

	for _, alias := range orderedAliases {
		table, ok := scope.tables[alias]
		if !ok {
			continue
		}
		for _, col := range table.Columns {
			ref := &ast.ColumnRef{
				Table:         alias,
				Column:        col.Name,
				ResolvedTable: alias,
				ResolvedIndex: col.Index,
				ResolvedType:  col.Type.String(),
			}
			expanded = append(expanded, ref)
		}
	}

	// Replace columns: for non-star entries, keep them; for star, insert expansion
	var newCols []ast.Expression
	for _, col := range sel.Columns {
		if _, ok := col.(*ast.StarExpr); ok {
			newCols = append(newCols, expanded...)
		} else {
			newCols = append(newCols, col)
		}
	}
	sel.Columns = newCols
	return nil
}

// resolveExpr walks an expression, annotating ColumnRef nodes.
// allowAgg controls whether aggregate functions are permitted.
func (a *Analyzer) resolveExpr(expr ast.Expression, scope *Scope, outerScope *Scope, allowAgg bool) error {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.ColumnRef:
		return a.resolveColumnRef(e, scope, outerScope)

	case *ast.AliasExpr:
		return a.resolveExpr(e.Expr, scope, outerScope, allowAgg)

	case *ast.BinaryExpr:
		if err := a.resolveExpr(e.Left, scope, outerScope, allowAgg); err != nil {
			return err
		}
		return a.resolveExpr(e.Right, scope, outerScope, allowAgg)

	case *ast.UnaryExpr:
		return a.resolveExpr(e.Expr, scope, outerScope, allowAgg)

	case *ast.FunctionCall:
		if isAggregate(e.Name) && !allowAgg {
			return analysisErrorf(e.Pos, "aggregate function %s not allowed here", e.Name)
		}
		if e.StarArg {
			return nil
		}
		for _, arg := range e.Args {
			if err := a.resolveExpr(arg, scope, outerScope, allowAgg); err != nil {
				return err
			}
		}
		return nil

	case *ast.CaseExpr:
		if e.Operand != nil {
			if err := a.resolveExpr(e.Operand, scope, outerScope, allowAgg); err != nil {
				return err
			}
		}
		for _, w := range e.Whens {
			if err := a.resolveExpr(w.Condition, scope, outerScope, allowAgg); err != nil {
				return err
			}
			if err := a.resolveExpr(w.Result, scope, outerScope, allowAgg); err != nil {
				return err
			}
		}
		return a.resolveExpr(e.ElseExpr, scope, outerScope, allowAgg)

	case *ast.InExpr:
		if err := a.resolveExpr(e.Expr, scope, outerScope, allowAgg); err != nil {
			return err
		}
		if e.Subquery != nil {
			_, err := a.analyzeSelect(e.Subquery, scope) // scope is outer scope for correlated
			return err
		}
		for _, item := range e.List {
			if err := a.resolveExpr(item, scope, outerScope, allowAgg); err != nil {
				return err
			}
		}
		return nil

	case *ast.BetweenExpr:
		if err := a.resolveExpr(e.Expr, scope, outerScope, allowAgg); err != nil {
			return err
		}
		if err := a.resolveExpr(e.Low, scope, outerScope, allowAgg); err != nil {
			return err
		}
		return a.resolveExpr(e.High, scope, outerScope, allowAgg)

	case *ast.IsNullExpr:
		return a.resolveExpr(e.Expr, scope, outerScope, allowAgg)

	case *ast.SubqueryExpr:
		_, err := a.analyzeSelect(e.Select, scope)
		return err

	case *ast.ExistsExpr:
		_, err := a.analyzeSelect(e.Subquery, scope)
		return err

	case *ast.CastExpr:
		return a.resolveExpr(e.Expr, scope, outerScope, allowAgg)

	case *ast.ExtractExpr:
		return a.resolveExpr(e.From, scope, outerScope, allowAgg)

	case *ast.WindowFuncExpr:
		// Resolve the inner function's arguments
		for _, arg := range e.Func.Args {
			if err := a.resolveExpr(arg, scope, outerScope, true); err != nil {
				return err
			}
		}
		// Resolve PARTITION BY and ORDER BY expressions
		if e.Over != nil {
			for _, pb := range e.Over.PartitionBy {
				if err := a.resolveExpr(pb, scope, outerScope, false); err != nil {
					return err
				}
			}
			for _, ob := range e.Over.OrderBy {
				if err := a.resolveExpr(ob.Expr, scope, outerScope, false); err != nil {
					return err
				}
			}
		}
		return nil

	case *ast.StarExpr, *ast.IntLiteral, *ast.FloatLiteral,
		*ast.StringLiteral, *ast.BoolLiteral, *ast.NullLiteral:
		return nil

	default:
		return fmt.Errorf("unknown expression type: %T", expr)
	}
}

func (a *Analyzer) resolveColumnRef(ref *ast.ColumnRef, scope *Scope, outerScope *Scope) error {
	alias, col, err := scope.resolveColumn(ref.Column, ref.Table)
	if err != nil {
		// Try outer scope for correlated subqueries
		if outerScope != nil {
			alias, col, err = outerScope.resolveColumn(ref.Column, ref.Table)
			if err != nil {
				return analysisErrorf(ref.Pos, "%s", err.Error())
			}
		} else {
			return analysisErrorf(ref.Pos, "%s", err.Error())
		}
	}
	ref.ResolvedTable = alias
	ref.ResolvedIndex = col.Index
	ref.ResolvedType = col.Type.String()
	return nil
}

func (a *Analyzer) analyzeCreateTable(stmt *ast.CreateTableStatement) error {
	// Check for duplicate column names
	seen := make(map[string]bool)
	for _, col := range stmt.Columns {
		lower := strings.ToLower(col.Name)
		if seen[lower] {
			return analysisErrorf(col.Pos, "duplicate column name %q in CREATE TABLE", col.Name)
		}
		seen[lower] = true

		// Validate type name
		if _, err := catalog.ParseDataType(col.TypeName); err != nil {
			return analysisErrorf(col.Pos, "invalid type %q for column %q", col.TypeName, col.Name)
		}
	}
	return nil
}

func (a *Analyzer) analyzeInsert(stmt *ast.InsertStatement) error {
	table, ok := a.catalog.Lookup(stmt.Table)
	if !ok {
		return analysisErrorf(stmt.Pos, "table %q not found", stmt.Table)
	}
	// Validate each column exists
	for _, colName := range stmt.Columns {
		if table.FindColumn(colName) == nil {
			return analysisErrorf(stmt.Pos, "column %q not found in table %q", colName, stmt.Table)
		}
	}
	// For INSERT ... SELECT, skip value-count validation.
	if stmt.SelectStmt != nil {
		_, err := a.analyzeSelect(stmt.SelectStmt, nil)
		return err
	}
	// Validate all value rows have matching column counts when a column list is given.
	rows := stmt.ValueRows
	if len(rows) == 0 {
		rows = [][]ast.Expression{stmt.Values}
	}
	if len(stmt.Columns) > 0 {
		for _, row := range rows {
			if len(stmt.Columns) != len(row) {
				return analysisErrorf(stmt.Pos,
					"column count (%d) doesn't match value count (%d)",
					len(stmt.Columns), len(row))
			}
		}
	}
	return nil
}

func (a *Analyzer) analyzeUpdate(stmt *ast.UpdateStatement) error {
	table, ok := a.catalog.Lookup(stmt.Table)
	if !ok {
		return analysisErrorf(stmt.Pos, "table %q not found", stmt.Table)
	}
	// Validate each assigned column exists
	for _, assign := range stmt.Assigns {
		if table.FindColumn(assign.Column) == nil {
			return analysisErrorf(stmt.Pos, "column %q not found in table %q", assign.Column, stmt.Table)
		}
	}
	return nil
}

func (a *Analyzer) analyzeDelete(stmt *ast.DeleteStatement) error {
	if _, ok := a.catalog.Lookup(stmt.Table); !ok {
		return analysisErrorf(stmt.Pos, "table %q not found", stmt.Table)
	}
	return nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func isAggregate(name string) bool {
	switch strings.ToUpper(name) {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		return true
	}
	return false
}

func containsWindowFunc(expr ast.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.WindowFuncExpr:
		_ = e
		return true
	case *ast.FunctionCall:
		for _, arg := range e.Args {
			if containsWindowFunc(arg) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return containsWindowFunc(e.Left) || containsWindowFunc(e.Right)
	case *ast.UnaryExpr:
		return containsWindowFunc(e.Expr)
	case *ast.AliasExpr:
		return containsWindowFunc(e.Expr)
	case *ast.CaseExpr:
		if containsWindowFunc(e.Operand) {
			return true
		}
		for _, w := range e.Whens {
			if containsWindowFunc(w.Condition) || containsWindowFunc(w.Result) {
				return true
			}
		}
		return containsWindowFunc(e.ElseExpr)
	case *ast.InExpr:
		if containsWindowFunc(e.Expr) {
			return true
		}
		for _, item := range e.List {
			if containsWindowFunc(item) {
				return true
			}
		}
	case *ast.BetweenExpr:
		return containsWindowFunc(e.Expr) || containsWindowFunc(e.Low) || containsWindowFunc(e.High)
	case *ast.IsNullExpr:
		return containsWindowFunc(e.Expr)
	}
	return false
}

func containsAggregate(expr ast.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.FunctionCall:
		return isAggregate(e.Name)
	case *ast.BinaryExpr:
		return containsAggregate(e.Left) || containsAggregate(e.Right)
	case *ast.UnaryExpr:
		return containsAggregate(e.Expr)
	case *ast.AliasExpr:
		return containsAggregate(e.Expr)
	case *ast.CaseExpr:
		if containsAggregate(e.Operand) {
			return true
		}
		for _, w := range e.Whens {
			if containsAggregate(w.Condition) || containsAggregate(w.Result) {
				return true
			}
		}
		return containsAggregate(e.ElseExpr)
	case *ast.InExpr:
		if containsAggregate(e.Expr) {
			return true
		}
		for _, item := range e.List {
			if containsAggregate(item) {
				return true
			}
		}
		return false
	case *ast.BetweenExpr:
		return containsAggregate(e.Expr) || containsAggregate(e.Low) || containsAggregate(e.High)
	case *ast.IsNullExpr:
		return containsAggregate(e.Expr)
	}
	return false
}

func isStarExpr(expr ast.Expression) bool {
	_, ok := expr.(*ast.StarExpr)
	if ok {
		return true
	}
	if alias, ok := expr.(*ast.AliasExpr); ok {
		_, ok = alias.Expr.(*ast.StarExpr)
		return ok
	}
	return false
}

// isKeyword checks if a token is a SQL keyword used as NOT operator.
func isNOT(tok lexer.Token) bool {
	return tok.Type == lexer.NOT
}

// extractSelectColumns infers the output column schema from a SELECT statement's
// column list. Used to build synthetic table schemas for CTEs and derived tables.
// A generic unknown-type column is created for each named output; star expressions
// are skipped (the caller will use a permissive scope instead).
func extractSelectColumns(sel *ast.SelectStatement) []catalog.Column {
	var cols []catalog.Column
	for i, expr := range sel.Columns {
		name := selectColName(expr, i)
		if name == "" {
			continue // skip star / unnamed
		}
		cols = append(cols, catalog.Column{Name: name, Type: catalog.TypeText, Index: len(cols)})
	}
	return cols
}

// selectColName returns the output column name for a SELECT expression.
func selectColName(expr ast.Expression, idx int) string {
	switch e := expr.(type) {
	case *ast.AliasExpr:
		return strings.ToLower(e.Alias)
	case *ast.ColumnRef:
		return strings.ToLower(e.Column)
	case *ast.FunctionCall:
		return strings.ToLower(e.Name)
	case *ast.StarExpr:
		return "" // handled by expandStar
	default:
		return fmt.Sprintf("col%d", idx)
	}
}
