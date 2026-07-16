package ast

import "github.com/query-engine/query-engine/internal/lexer"

// Pos holds source position information.
type Pos struct {
	Line int
	Col  int
}

// Statement is the top-level interface for all SQL statements.
type Statement interface {
	statementNode()
	GetPos() Pos
}

// Expression is the interface for all expression nodes.
type Expression interface {
	expressionNode()
	GetPos() Pos
}

// -----------------------------------------------------------------------
// Statements
// -----------------------------------------------------------------------

// CTEDef is a single WITH [RECURSIVE] name AS (...) entry.
type CTEDef struct {
	Name            string
	Select          *SelectStatement  // base (non-recursive) part; may be nil if BodyStmt is set
	BodyStmt        Statement         // full CTE body when it is a set-op (non-recursive UNION/INTERSECT/EXCEPT)
	Recursive       bool              // true for WITH RECURSIVE
	RecursiveSelect *SelectStatement  // recursive part (after UNION [ALL]) — nil if not recursive
	RecursiveAll    bool              // true if the UNION is UNION ALL
}

// SelectStatement represents a full SELECT query.
type SelectStatement struct {
	Pos      Pos
	CTEs     []CTEDef     // WITH clause entries (populated on the outermost SELECT)
	Distinct bool
	Columns  []Expression // SelectExpr list or StarExpr
	From     *TableRef
	Joins    []*JoinClause
	Where    Expression // nil if absent
	GroupBy  []Expression
	Having   Expression // nil if absent
	OrderBy  []*SortSpec
	Limit    Expression // nil if absent
	Offset   Expression // nil if absent
}

func (s *SelectStatement) statementNode() {}
func (s *SelectStatement) GetPos() Pos    { return s.Pos }

// CreateTableStatement represents CREATE TABLE.
type CreateTableStatement struct {
	Pos        Pos
	Name       string
	Columns    []*ColumnDef
	SelectStmt *SelectStatement // non-nil for CREATE TABLE ... AS SELECT
}

func (c *CreateTableStatement) statementNode() {}
func (c *CreateTableStatement) GetPos() Pos    { return c.Pos }

// InsertStatement represents INSERT INTO ... VALUES (...)[, (...)] or INSERT INTO ... SELECT ...
type InsertStatement struct {
	Pos        Pos
	Table      string
	Columns    []string
	Values     []Expression   // single-row shorthand (first row); kept for backwards compat
	ValueRows  [][]Expression // all rows including Values[0] when parsing multi-row INSERT
	SelectStmt *SelectStatement // non-nil for INSERT ... SELECT
}

func (i *InsertStatement) statementNode() {}
func (i *InsertStatement) GetPos() Pos    { return i.Pos }

// UpdateStatement represents UPDATE table SET col=expr [WHERE cond].
type UpdateStatement struct {
	Pos     Pos
	Table   string
	Assigns []UpdateAssign
	Where   Expression
}

func (u *UpdateStatement) statementNode() {}
func (u *UpdateStatement) GetPos() Pos    { return u.Pos }

// UpdateAssign is a single col = expr assignment in an UPDATE.
type UpdateAssign struct {
	Column string
	Value  Expression
}

// DeleteStatement represents DELETE FROM table [WHERE cond].
type DeleteStatement struct {
	Pos   Pos
	Table string
	Where Expression
}

func (d *DeleteStatement) statementNode() {}
func (d *DeleteStatement) GetPos() Pos    { return d.Pos }

// SetOpStatement represents SELECT ... UNION/INTERSECT/EXCEPT SELECT ...
type SetOpStatement struct {
	Pos   Pos
	Op    string // "UNION", "INTERSECT", "EXCEPT"
	All   bool   // true for UNION ALL / INTERSECT ALL / EXCEPT ALL
	Left  Statement
	Right *SelectStatement
}

func (s *SetOpStatement) statementNode() {}
func (s *SetOpStatement) GetPos() Pos    { return s.Pos }

// ColumnDef is a column definition inside CREATE TABLE.
type ColumnDef struct {
	Pos        Pos
	Name       string
	TypeName   string
	NotNull    bool
	PrimaryKey bool
	DefaultVal Expression // optional DEFAULT expression
}

// -----------------------------------------------------------------------
// Table references
// -----------------------------------------------------------------------

// TableRef is a FROM clause entry (table name or subquery with optional alias).
type TableRef struct {
	Pos      Pos
	Name     string // "" if subquery
	Alias    string
	Subquery *SelectStatement // non-nil if derived table
}

// JoinType represents the type of JOIN.
type JoinType int

const (
	JoinInner JoinType = iota
	JoinLeft
	JoinRight
	JoinCross
	JoinFull
)

func (jt JoinType) String() string {
	switch jt {
	case JoinInner:
		return "INNER"
	case JoinLeft:
		return "LEFT"
	case JoinRight:
		return "RIGHT"
	case JoinCross:
		return "CROSS"
	case JoinFull:
		return "FULL"
	default:
		return "UNKNOWN"
	}
}

// JoinClause is a single JOIN ... ON ... / USING (...) / NATURAL clause.
type JoinClause struct {
	Pos       Pos
	JoinType  JoinType
	Table     *TableRef
	Condition Expression // nil for CROSS JOIN, NATURAL JOIN, USING
	Using     []string   // column names for JOIN ... USING (col, ...)
	Natural   bool       // true for NATURAL [INNER|LEFT] JOIN
}

// SortSpec is an ORDER BY expression with direction and NULL ordering.
type SortSpec struct {
	Expr           Expression
	Ascending      bool // true = ASC (default), false = DESC
	NullsFirst     bool // true = NULLS FIRST, false = NULLS LAST
	NullsSpecified bool // whether NULLS FIRST/LAST was explicitly written
}

// -----------------------------------------------------------------------
// Expression nodes
// -----------------------------------------------------------------------

// StarExpr represents *.
type StarExpr struct {
	Pos Pos
}

func (s *StarExpr) expressionNode() {}
func (s *StarExpr) GetPos() Pos     { return s.Pos }

// AliasExpr wraps an expression with an AS alias.
type AliasExpr struct {
	Pos   Pos
	Expr  Expression
	Alias string
}

func (a *AliasExpr) expressionNode() {}
func (a *AliasExpr) GetPos() Pos     { return a.Pos }

// ColumnRef is a table.column or bare column reference.
type ColumnRef struct {
	Pos        Pos
	Table      string // "" if unqualified
	Column     string
	// Resolved by analyzer:
	ResolvedTable  string
	ResolvedIndex  int
	ResolvedType   string
}

func (c *ColumnRef) expressionNode() {}
func (c *ColumnRef) GetPos() Pos     { return c.Pos }

// IntLiteral is an integer constant.
type IntLiteral struct {
	Pos   Pos
	Value int64
}

func (i *IntLiteral) expressionNode() {}
func (i *IntLiteral) GetPos() Pos     { return i.Pos }

// FloatLiteral is a floating-point constant.
type FloatLiteral struct {
	Pos   Pos
	Value float64
}

func (f *FloatLiteral) expressionNode() {}
func (f *FloatLiteral) GetPos() Pos     { return f.Pos }

// StringLiteral is a string constant.
type StringLiteral struct {
	Pos   Pos
	Value string
}

func (s *StringLiteral) expressionNode() {}
func (s *StringLiteral) GetPos() Pos     { return s.Pos }

// BoolLiteral is TRUE or FALSE.
type BoolLiteral struct {
	Pos   Pos
	Value bool
}

func (b *BoolLiteral) expressionNode() {}
func (b *BoolLiteral) GetPos() Pos     { return b.Pos }

// NullLiteral is NULL.
type NullLiteral struct {
	Pos Pos
}

func (n *NullLiteral) expressionNode() {}
func (n *NullLiteral) GetPos() Pos     { return n.Pos }

// BinaryExpr is left OP right.
type BinaryExpr struct {
	Pos        Pos
	Left       Expression
	Op         lexer.Token
	Right      Expression
	EscapeChar string // non-empty for LIKE ... ESCAPE 'c'
}

func (b *BinaryExpr) expressionNode() {}
func (b *BinaryExpr) GetPos() Pos     { return b.Pos }

// UnaryExpr is OP expr (NOT, unary minus).
type UnaryExpr struct {
	Pos  Pos
	Op   lexer.Token
	Expr Expression
}

func (u *UnaryExpr) expressionNode() {}
func (u *UnaryExpr) GetPos() Pos     { return u.Pos }

// FunctionCall is name(args).
type FunctionCall struct {
	Pos      Pos
	Name     string
	Args     []Expression
	StarArg  bool // COUNT(*) — star argument
	Distinct bool // COUNT(DISTINCT col)
}

func (f *FunctionCall) expressionNode() {}
func (f *FunctionCall) GetPos() Pos     { return f.Pos }

// CaseExpr is CASE WHEN ... THEN ... ELSE ... END.
type WhenClause struct {
	Condition Expression
	Result    Expression
}

type CaseExpr struct {
	Pos      Pos
	Operand  Expression  // nil for searched CASE
	Whens    []WhenClause
	ElseExpr Expression // nil if absent
}

func (c *CaseExpr) expressionNode() {}
func (c *CaseExpr) GetPos() Pos     { return c.Pos }

// InExpr is expr IN (list) or expr IN (subquery).
type InExpr struct {
	Pos      Pos
	Expr     Expression
	List     []Expression      // non-empty if list form
	Subquery *SelectStatement  // non-nil if subquery form
	Negated  bool              // NOT IN
}

func (i *InExpr) expressionNode() {}
func (i *InExpr) GetPos() Pos     { return i.Pos }

// BetweenExpr is expr BETWEEN low AND high.
type BetweenExpr struct {
	Pos     Pos
	Expr    Expression
	Low     Expression
	High    Expression
	Negated bool // NOT BETWEEN
}

func (b *BetweenExpr) expressionNode() {}
func (b *BetweenExpr) GetPos() Pos     { return b.Pos }

// IsNullExpr is expr IS NULL or expr IS NOT NULL.
type IsNullExpr struct {
	Pos     Pos
	Expr    Expression
	Negated bool // IS NOT NULL
}

func (i *IsNullExpr) expressionNode() {}
func (i *IsNullExpr) GetPos() Pos     { return i.Pos }

// SubqueryExpr wraps a SELECT for use as an expression.
type SubqueryExpr struct {
	Pos    Pos
	Select *SelectStatement
}

func (s *SubqueryExpr) expressionNode() {}
func (s *SubqueryExpr) GetPos() Pos     { return s.Pos }

// ExistsExpr is EXISTS (subquery).
type ExistsExpr struct {
	Pos      Pos
	Subquery *SelectStatement
	Negated  bool
}

func (e *ExistsExpr) expressionNode() {}
func (e *ExistsExpr) GetPos() Pos     { return e.Pos }

// CastExpr is CAST(expr AS typename).
type CastExpr struct {
	Pos      Pos
	Expr     Expression
	TypeName string // "INT", "FLOAT", "TEXT", "BOOL"
}

func (c *CastExpr) expressionNode() {}
func (c *CastExpr) GetPos() Pos     { return c.Pos }

// ExplainStatement represents EXPLAIN [ANALYZE] stmt.
type ExplainStatement struct {
	Pos     Pos
	Analyze bool
	Stmt    Statement
}

func (e *ExplainStatement) statementNode() {}
func (e *ExplainStatement) GetPos() Pos    { return e.Pos }

// DropTableStatement represents DROP TABLE [IF EXISTS] name.
type DropTableStatement struct {
	Pos      Pos
	Name     string
	IfExists bool
}

func (d *DropTableStatement) statementNode() {}
func (d *DropTableStatement) GetPos() Pos    { return d.Pos }

// AlterTableStatement represents ALTER TABLE DDL operations.
type AlterTableStatement struct {
	Pos        Pos
	Table      string
	Action     string     // "ADD", "DROP", "RENAME", "RENAME_COLUMN"
	ColDef     *ColumnDef // for ADD
	ColName    string     // for DROP / RENAME_COLUMN (old name)
	NewName    string     // for RENAME (table) / RENAME_COLUMN (new name)
}

func (a *AlterTableStatement) statementNode() {}
func (a *AlterTableStatement) GetPos() Pos    { return a.Pos }

// ExtractExpr is EXTRACT(part FROM expr).
type ExtractExpr struct {
	Pos  Pos
	Part string // YEAR, MONTH, DAY, HOUR, MINUTE, SECOND, DOW, DOY, EPOCH
	From Expression
}

func (e *ExtractExpr) expressionNode() {}
func (e *ExtractExpr) GetPos() Pos     { return e.Pos }

// -----------------------------------------------------------------------
// Window functions
// -----------------------------------------------------------------------

// FrameBoundKind identifies the kind of a window frame boundary.
type FrameBoundKind int

const (
	FrameUnboundedPreceding FrameBoundKind = iota
	FrameNPreceding
	FrameCurrentRow
	FrameNFollowing
	FrameUnboundedFollowing
)

// FrameBound is one side of a window frame.
type FrameBound struct {
	Kind   FrameBoundKind
	Offset Expression // non-nil for FrameNPreceding / FrameNFollowing
}

// WindowFrame is the ROWS/RANGE BETWEEN ... AND ... clause.
type WindowFrame struct {
	Mode  string     // "ROWS" or "RANGE"
	Start FrameBound
	End   FrameBound
}

// WindowSpec is the spec inside OVER (...).
type WindowSpec struct {
	PartitionBy []Expression
	OrderBy     []*SortSpec
	Frame       *WindowFrame // nil = default
}

// WindowFuncExpr is func([args]) OVER (window_spec).
type WindowFuncExpr struct {
	Pos  Pos
	Func *FunctionCall
	Over *WindowSpec
}

func (w *WindowFuncExpr) expressionNode() {}
func (w *WindowFuncExpr) GetPos() Pos     { return w.Pos }

