package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/lexer"
)

// Parser converts a token stream into an AST.
type Parser struct {
	lex     *lexer.Lexer
	current lexer.Token
	peek    lexer.Token
}

// New creates a parser for the given SQL input.
func New(input string) *Parser {
	p := &Parser{lex: lexer.New(input)}
	p.advance() // load current
	p.advance() // load peek
	return p
}

// ParseStatement parses one SQL statement.
func (p *Parser) ParseStatement() (ast.Statement, error) {
	// Skip leading semicolons so multiple-statement callers can call ParseStatement
	// repeatedly after each statement ends with ";".
	for p.current.Type == lexer.SEMI {
		p.advance()
	}
	switch p.current.Type {
	case lexer.SELECT:
		return p.parseSelectOrSetOp()
	case lexer.WITH:
		return p.parseWithClause()
	case lexer.CREATE:
		return p.parseCreateTable()
	case lexer.INSERT:
		return p.parseInsert()
	case lexer.UPDATE:
		return p.parseUpdate()
	case lexer.DELETE:
		return p.parseDelete()
	case lexer.EXPLAIN:
		return p.parseExplain()
	case lexer.DROP:
		return p.parseDropTable()
	case lexer.ALTER:
		return p.parseAlterTable()
	default:
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected SELECT, CREATE, INSERT, UPDATE, DELETE, WITH, EXPLAIN, DROP, or ALTER, got %s", p.current.Literal)
	}
}

// parseSelectOrSetOp parses SELECT and any trailing UNION/INTERSECT/EXCEPT.
func (p *Parser) parseSelectOrSetOp() (ast.Statement, error) {
	left, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	return p.parseSetOp(left)
}

// parseSetOp checks for UNION/INTERSECT/EXCEPT after a query and builds the tree.
func (p *Parser) parseSetOp(left ast.Statement) (ast.Statement, error) {
	var op string
	switch p.current.Type {
	case lexer.UNION:
		op = "UNION"
	case lexer.INTERSECT:
		op = "INTERSECT"
	case lexer.EXCEPT:
		op = "EXCEPT"
	default:
		return left, nil
	}
	startTok := p.current
	p.advance() // consume UNION/INTERSECT/EXCEPT

	all := false
	if p.current.Type == lexer.ALL {
		all = true
		p.advance()
	}

	right, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	node := &ast.SetOpStatement{
		Pos:   ast.Pos{Line: startTok.Line, Col: startTok.Col},
		Op:    op,
		All:   all,
		Left:  left,
		Right: right,
	}
	// Check for chained set operations
	return p.parseSetOp(node)
}

// -----------------------------------------------------------------------
// Token management helpers
// -----------------------------------------------------------------------

func (p *Parser) advance() {
	p.current = p.peek
	p.peek = p.lex.Next()
}

func (p *Parser) check(tt lexer.TokenType) bool {
	return p.current.Type == tt
}

func (p *Parser) peekIs(tt lexer.TokenType) bool {
	return p.peek.Type == tt
}

func (p *Parser) match(types ...lexer.TokenType) bool {
	for _, tt := range types {
		if p.current.Type == tt {
			p.advance()
			return true
		}
	}
	return false
}

// expect consumes the current token if it matches tt, otherwise returns an error.
func (p *Parser) expect(tt lexer.TokenType) (lexer.Token, error) {
	if p.current.Type != tt {
		return lexer.Token{}, parseErrorf(p.current.Line, p.current.Col,
			"expected %s, got %s (%q)", tt, p.current.Type, p.current.Literal)
	}
	tok := p.current
	p.advance()
	return tok, nil
}

func pos(tok lexer.Token) ast.Pos {
	return ast.Pos{Line: tok.Line, Col: tok.Col}
}

// -----------------------------------------------------------------------
// SELECT
// -----------------------------------------------------------------------

func (p *Parser) parseSelect() (*ast.SelectStatement, error) {
	startTok := p.current
	if _, err := p.expect(lexer.SELECT); err != nil {
		return nil, err
	}

	stmt := &ast.SelectStatement{Pos: pos(startTok)}

	// DISTINCT
	if p.check(lexer.DISTINCT) {
		stmt.Distinct = true
		p.advance()
	}

	// Column list
	cols, err := p.parseSelectColumns()
	if err != nil {
		return nil, err
	}
	stmt.Columns = cols

	// FROM
	if p.check(lexer.FROM) {
		p.advance()
		tableRef, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		stmt.From = tableRef
	} else if !p.check(lexer.EOF) && !p.check(lexer.SEMI) &&
		!p.check(lexer.RPAREN) && !p.check(lexer.UNION) &&
		!p.check(lexer.INTERSECT) && !p.check(lexer.EXCEPT) &&
		!p.check(lexer.WHERE) && !p.check(lexer.GROUP) &&
		!p.check(lexer.ORDER) && !p.check(lexer.HAVING) &&
		!p.check(lexer.LIMIT) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected FROM after select list, got %s", p.current.Literal)
	}

	// JOINs
	for p.isJoinKeyword() {
		j, err := p.parseJoin()
		if err != nil {
			return nil, err
		}
		stmt.Joins = append(stmt.Joins, j)
	}

	// WHERE
	if p.check(lexer.WHERE) {
		p.advance()
		expr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		stmt.Where = expr
	}

	// GROUP BY
	if p.check(lexer.GROUP) && p.peek.Type == lexer.BY {
		p.advance() // GROUP
		p.advance() // BY
		groupBy, err := p.parseExpressionList()
		if err != nil {
			return nil, err
		}
		stmt.GroupBy = groupBy

		// HAVING
		if p.check(lexer.HAVING) {
			p.advance()
			having, err := p.parseExpression(0)
			if err != nil {
				return nil, err
			}
			stmt.Having = having
		}
	}

	// ORDER BY
	if p.check(lexer.ORDER) && p.peek.Type == lexer.BY {
		p.advance() // ORDER
		p.advance() // BY
		sortSpecs, err := p.parseSortSpecs()
		if err != nil {
			return nil, err
		}
		stmt.OrderBy = sortSpecs
	}

	// LIMIT
	if p.check(lexer.LIMIT) {
		p.advance()
		limit, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		stmt.Limit = limit
	}

	// OFFSET
	if p.check(lexer.OFFSET) {
		p.advance()
		offset, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		stmt.Offset = offset
	}

	return stmt, nil
}

func (p *Parser) parseSelectColumns() ([]ast.Expression, error) {
	// Handle SELECT * shorthand
	if p.check(lexer.STAR) {
		starTok := p.current
		p.advance()
		return []ast.Expression{&ast.StarExpr{Pos: pos(starTok)}}, nil
	}

	var cols []ast.Expression
	for {
		expr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}

		// Optional AS alias
		if p.check(lexer.AS) {
			aliasTok := p.current
			p.advance()
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected alias name after AS, got %s", p.current.Literal)
			}
			alias := p.current.Literal
			p.advance()
			cols = append(cols, &ast.AliasExpr{Pos: pos(aliasTok), Expr: expr, Alias: alias})
		} else if (p.check(lexer.IDENT) || isKeyword(p.current.Type)) &&
			!p.check(lexer.FROM) && !p.check(lexer.WHERE) &&
			!p.check(lexer.GROUP) && !p.check(lexer.ORDER) &&
			!p.check(lexer.LIMIT) && !p.check(lexer.HAVING) &&
			!p.check(lexer.UNION) && !p.check(lexer.INTERSECT) &&
			!p.check(lexer.EXCEPT) && !p.check(lexer.COMMA) &&
			!p.check(lexer.EOF) && !p.check(lexer.RPAREN) && !p.check(lexer.SEMI) {
			// Implicit alias: expr alias (without AS keyword)
			// Check that the next token is a bare identifier (not a keyword that starts a clause)
			if p.check(lexer.IDENT) {
				alias := p.current.Literal
				p.advance()
				cols = append(cols, &ast.AliasExpr{Pos: expr.GetPos(), Expr: expr, Alias: alias})
			} else {
				cols = append(cols, expr)
			}
		} else {
			cols = append(cols, expr)
		}

		if !p.check(lexer.COMMA) {
			break
		}
		p.advance() // consume comma
	}
	return cols, nil
}

func (p *Parser) parseTableRef() (*ast.TableRef, error) {
	startTok := p.current

	// Subquery: (SELECT ...)
	if p.check(lexer.LPAREN) {
		p.advance()
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		alias := ""
		if p.check(lexer.AS) {
			p.advance()
		}
		if p.check(lexer.IDENT) || isKeyword(p.current.Type) {
			alias = p.current.Literal
			p.advance()
		} else {
			return nil, parseErrorf(p.current.Line, p.current.Col,
				"subquery in FROM requires an alias")
		}
		return &ast.TableRef{Pos: pos(startTok), Subquery: sub, Alias: alias}, nil
	}

	// Plain table name
	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name, got %s", p.current.Literal)
	}
	name := p.current.Literal
	nameTok := p.current
	p.advance()

	alias := ""
	if p.check(lexer.AS) {
		p.advance()
		if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
			return nil, parseErrorf(p.current.Line, p.current.Col,
				"expected alias after AS, got %s", p.current.Literal)
		}
		alias = p.current.Literal
		p.advance()
	} else if p.check(lexer.IDENT) && !p.isJoinKeyword() && !isClauseKeyword(p.current.Type) {
		// Implicit alias
		alias = p.current.Literal
		p.advance()
	}

	return &ast.TableRef{Pos: pos(nameTok), Name: name, Alias: alias}, nil
}

func (p *Parser) isJoinKeyword() bool {
	switch p.current.Type {
	case lexer.JOIN, lexer.INNER, lexer.LEFT, lexer.RIGHT, lexer.CROSS, lexer.FULL, lexer.NATURAL:
		return true
	}
	return false
}

func isClauseKeyword(tt lexer.TokenType) bool {
	switch tt {
	case lexer.WHERE, lexer.GROUP, lexer.ORDER, lexer.HAVING,
		lexer.LIMIT, lexer.OFFSET, lexer.JOIN, lexer.INNER,
		lexer.LEFT, lexer.RIGHT, lexer.CROSS, lexer.FULL, lexer.NATURAL,
		lexer.ON, lexer.USING,
		lexer.UNION, lexer.INTERSECT, lexer.EXCEPT,
		lexer.EOF, lexer.SEMI, lexer.RPAREN:
		return true
	}
	return false
}

func (p *Parser) parseJoin() (*ast.JoinClause, error) {
	startTok := p.current
	joinType := ast.JoinInner
	natural := false

	// NATURAL [INNER|LEFT] JOIN
	if p.current.Type == lexer.NATURAL {
		natural = true
		p.advance()
		// optional INNER / LEFT after NATURAL
		if p.current.Type == lexer.LEFT {
			joinType = ast.JoinLeft
			p.advance()
		} else if p.current.Type == lexer.INNER {
			p.advance()
		}
		if _, err := p.expect(lexer.JOIN); err != nil {
			return nil, err
		}
	} else {
		switch p.current.Type {
		case lexer.JOIN:
			p.advance()
		case lexer.INNER:
			p.advance()
			if _, err := p.expect(lexer.JOIN); err != nil {
				return nil, err
			}
		case lexer.LEFT:
			joinType = ast.JoinLeft
			p.advance()
			if p.current.Type == lexer.OUTER {
				p.advance()
			}
			if _, err := p.expect(lexer.JOIN); err != nil {
				return nil, err
			}
		case lexer.RIGHT:
			joinType = ast.JoinRight
			p.advance()
			if p.current.Type == lexer.OUTER {
				p.advance()
			}
			if _, err := p.expect(lexer.JOIN); err != nil {
				return nil, err
			}
		case lexer.CROSS:
			joinType = ast.JoinCross
			p.advance()
			if _, err := p.expect(lexer.JOIN); err != nil {
				return nil, err
			}
		case lexer.FULL:
			joinType = ast.JoinFull
			p.advance()
			if p.current.Type == lexer.OUTER {
				p.advance()
			}
			if _, err := p.expect(lexer.JOIN); err != nil {
				return nil, err
			}
		}
	}

	tableRef, err := p.parseTableRef()
	if err != nil {
		return nil, err
	}

	jc := &ast.JoinClause{
		Pos:      pos(startTok),
		JoinType: joinType,
		Table:    tableRef,
		Natural:  natural,
	}

	if natural {
		// NATURAL JOIN has no ON or USING clause — condition derived automatically
		return jc, nil
	}

	if joinType == ast.JoinCross {
		// CROSS JOIN has no condition
		return jc, nil
	}

	// ON condition or USING (cols)
	if p.current.Type == lexer.USING {
		p.advance()
		if _, err := p.expect(lexer.LPAREN); err != nil {
			return nil, err
		}
		for {
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected column name in USING, got %s", p.current.Literal)
			}
			jc.Using = append(jc.Using, p.current.Literal)
			p.advance()
			if !p.check(lexer.COMMA) {
				break
			}
			p.advance()
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return jc, nil
	}

	if _, err := p.expect(lexer.ON); err != nil {
		return nil, err
	}
	cond, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	jc.Condition = cond
	return jc, nil
}

func (p *Parser) parseExpressionList() ([]ast.Expression, error) {
	var exprs []ast.Expression
	for {
		expr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
		if !p.check(lexer.COMMA) {
			break
		}
		p.advance()
	}
	return exprs, nil
}

func (p *Parser) parseSortSpecs() ([]*ast.SortSpec, error) {
	var specs []*ast.SortSpec
	for {
		expr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		asc := true
		if p.check(lexer.DESC) {
			asc = false
			p.advance()
		} else if p.check(lexer.ASC) {
			p.advance()
		}
		spec := &ast.SortSpec{Expr: expr, Ascending: asc}
		// NULLS FIRST / NULLS LAST
		if p.check(lexer.NULLS) {
			p.advance()
			if p.check(lexer.FIRST) {
				spec.NullsFirst = true
				spec.NullsSpecified = true
				p.advance()
			} else if p.check(lexer.LAST) {
				spec.NullsFirst = false
				spec.NullsSpecified = true
				p.advance()
			} else {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected FIRST or LAST after NULLS, got %s", p.current.Literal)
			}
		}
		specs = append(specs, spec)
		if !p.check(lexer.COMMA) {
			break
		}
		p.advance()
	}
	return specs, nil
}

// -----------------------------------------------------------------------
// Expression parsing (Pratt / precedence climbing)
// -----------------------------------------------------------------------

// Operator precedence table (higher = binds tighter).
var precedence = map[lexer.TokenType]int{
	lexer.OR:      1,
	lexer.AND:     2,
	lexer.EQ:      4, lexer.NEQ: 4, lexer.LT: 4, lexer.GT: 4, lexer.LTE: 4, lexer.GTE: 4,
	lexer.LIKE:    4, lexer.IN: 4, lexer.BETWEEN: 4, lexer.IS: 4,
	lexer.PLUS:    5, lexer.MINUS: 5,
	lexer.STAR:    6, lexer.SLASH: 6, lexer.PERCENT: 6,
	lexer.CONCAT:  5,
}

func (p *Parser) parseExpression(minPrec int) (ast.Expression, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for {
		// Handle NOT IN, NOT BETWEEN, NOT LIKE as postfix operators
		if p.current.Type == lexer.NOT && (p.peek.Type == lexer.IN || p.peek.Type == lexer.BETWEEN || p.peek.Type == lexer.LIKE) {
			if 4 < minPrec {
				break
			}
			notTok := p.current
			p.advance() // consume NOT
			op := p.current
			p.advance() // consume IN, BETWEEN, or LIKE
			if op.Type == lexer.IN {
				result, err := p.parseIn(left, op, true)
				if err != nil {
					return nil, err
				}
				left = result
				continue
			}
			if op.Type == lexer.BETWEEN {
				result, err := p.parseBetween(left, op, true)
				if err != nil {
					return nil, err
				}
				left = result
				continue
			}
			// LIKE: parse pattern and wrap in NOT(left LIKE pattern)
			pattern, err := p.parseExpression(5)
			if err != nil {
				return nil, err
			}
			escapeChar := ""
			if p.current.Type == lexer.ESCAPE {
				p.advance() // consume ESCAPE
				escLit, err := p.parseExpression(5)
				if err != nil {
					return nil, err
				}
				if sl, ok := escLit.(*ast.StringLiteral); ok && len(sl.Value) == 1 {
					escapeChar = sl.Value
				}
			}
			likeExpr := &ast.BinaryExpr{Pos: pos(op), Left: left, Op: op, Right: pattern, EscapeChar: escapeChar}
			left = &ast.UnaryExpr{Pos: pos(notTok), Op: notTok, Expr: likeExpr}
			continue
		}

		prec, ok := precedence[p.current.Type]
		if !ok || prec < minPrec {
			break
		}

		op := p.current
		opType := op.Type
		p.advance()

		// Postfix / special binary forms
		switch opType {
		case lexer.IN:
			result, err := p.parseIn(left, op, false)
			if err != nil {
				return nil, err
			}
			left = result
		case lexer.BETWEEN:
			result, err := p.parseBetween(left, op, false)
			if err != nil {
				return nil, err
			}
			left = result
		case lexer.IS:
			result, err := p.parseIsNull(left, op)
			if err != nil {
				return nil, err
			}
			left = result
		case lexer.LIKE:
			right, err := p.parseExpression(prec + 1)
			if err != nil {
				return nil, err
			}
			escapeChar := ""
			if p.current.Type == lexer.ESCAPE {
				p.advance() // consume ESCAPE
				escLit, err := p.parseExpression(prec + 1)
				if err != nil {
					return nil, err
				}
				if sl, ok := escLit.(*ast.StringLiteral); ok && len(sl.Value) == 1 {
					escapeChar = sl.Value
				}
			}
			left = &ast.BinaryExpr{Pos: pos(op), Left: left, Op: op, Right: right, EscapeChar: escapeChar}
		default:
			right, err := p.parseExpression(prec + 1)
			if err != nil {
				return nil, err
			}
			left = &ast.BinaryExpr{Pos: pos(op), Left: left, Op: op, Right: right}
		}
	}

	return left, nil
}

func (p *Parser) parseUnary() (ast.Expression, error) {
	// NOT
	if p.check(lexer.NOT) {
		opTok := p.current
		p.advance()

		if p.check(lexer.EXISTS) {
			p.advance()
			if _, err := p.expect(lexer.LPAREN); err != nil {
				return nil, err
			}
			sub, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
			return &ast.ExistsExpr{Pos: pos(opTok), Subquery: sub, Negated: true}, nil
		}
		// Parse right-hand side at precedence 3 (above AND/OR but captures comparisons)
		// This makes NOT x = 5 parse as NOT (x = 5).
		expr, err := p.parseExpression(3)
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Pos: pos(opTok), Op: opTok, Expr: expr}, nil
	}

	// Unary minus
	if p.check(lexer.MINUS) {
		opTok := p.current
		p.advance()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Pos: pos(opTok), Op: opTok, Expr: expr}, nil
	}

	// EXISTS
	if p.check(lexer.EXISTS) {
		opTok := p.current
		p.advance()
		if _, err := p.expect(lexer.LPAREN); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return &ast.ExistsExpr{Pos: pos(opTok), Subquery: sub}, nil
	}

	return p.parsePrimary()
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	tok := p.current

	switch tok.Type {
	case lexer.INT_LIT:
		p.advance()
		v, err := strconv.ParseInt(tok.Literal, 10, 64)
		if err != nil {
			return nil, parseErrorf(tok.Line, tok.Col, "invalid integer: %s", tok.Literal)
		}
		return &ast.IntLiteral{Pos: pos(tok), Value: v}, nil

	case lexer.FLOAT_LIT:
		p.advance()
		v, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return nil, parseErrorf(tok.Line, tok.Col, "invalid float: %s", tok.Literal)
		}
		return &ast.FloatLiteral{Pos: pos(tok), Value: v}, nil

	case lexer.STRING_LIT:
		p.advance()
		return &ast.StringLiteral{Pos: pos(tok), Value: tok.Literal}, nil

	case lexer.TRUE:
		p.advance()
		return &ast.BoolLiteral{Pos: pos(tok), Value: true}, nil

	case lexer.FALSE:
		p.advance()
		return &ast.BoolLiteral{Pos: pos(tok), Value: false}, nil

	case lexer.NULL:
		p.advance()
		return &ast.NullLiteral{Pos: pos(tok)}, nil

	case lexer.STAR:
		p.advance()
		return &ast.StarExpr{Pos: pos(tok)}, nil

	case lexer.CASE:
		return p.parseCase()

	case lexer.CAST:
		return p.parseCast()

	case lexer.EXTRACT:
		return p.parseExtract()

	case lexer.LPAREN:
		p.advance()
		// Subquery?
		if p.check(lexer.SELECT) {
			sub, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
			return &ast.SubqueryExpr{Pos: pos(tok), Select: sub}, nil
		}
		// Grouped expression
		expr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return expr, nil

	case lexer.IDENT:
		name := tok.Literal
		p.advance()

		// Function call: name(...)
		if p.check(lexer.LPAREN) {
			return p.parseFunctionCall(tok, name)
		}

		// Qualified column: table.column
		if p.check(lexer.DOT) {
			p.advance()
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected column name after '.', got %s", p.current.Literal)
			}
			col := p.current.Literal
			p.advance()
			return &ast.ColumnRef{Pos: pos(tok), Table: name, Column: col}, nil
		}

		// Bare column reference
		return &ast.ColumnRef{Pos: pos(tok), Column: name}, nil

	default:
		// Some keywords can act as identifiers in certain contexts (column names, etc.)
		if isKeyword(tok.Type) && !isReservedExprKeyword(tok.Type) {
			name := tok.Literal
			p.advance()

			// Function call
			if p.check(lexer.LPAREN) {
				return p.parseFunctionCall(tok, strings.ToUpper(name))
			}
			// Qualified column
			if p.check(lexer.DOT) {
				p.advance()
				if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
					return nil, parseErrorf(p.current.Line, p.current.Col,
						"expected column name after '.', got %s", p.current.Literal)
				}
				col := p.current.Literal
				p.advance()
				return &ast.ColumnRef{Pos: pos(tok), Table: name, Column: col}, nil
			}
			return &ast.ColumnRef{Pos: pos(tok), Column: name}, nil
		}

		return nil, parseErrorf(tok.Line, tok.Col,
			"unexpected token in expression: %s (%q)", tok.Type, tok.Literal)
	}
}

func (p *Parser) parseFunctionCall(nameTok lexer.Token, name string) (ast.Expression, error) {
	p.advance() // consume '('
	fc := &ast.FunctionCall{Pos: pos(nameTok), Name: strings.ToUpper(name)}

	if p.check(lexer.RPAREN) {
		p.advance()
	} else {
		// COUNT(*) special case
		if p.check(lexer.STAR) {
			fc.StarArg = true
			p.advance()
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
		} else {
			// DISTINCT modifier: COUNT(DISTINCT col)
			if p.check(lexer.DISTINCT) {
				fc.Distinct = true
				p.advance()
			}

			args, err := p.parseExpressionList()
			if err != nil {
				return nil, err
			}
			fc.Args = args

			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
		}
	}

	// Check for OVER clause → window function
	if p.current.Type == lexer.OVER {
		p.advance() // consume OVER
		spec, err := p.parseWindowSpec()
		if err != nil {
			return nil, err
		}
		return &ast.WindowFuncExpr{Pos: pos(nameTok), Func: fc, Over: spec}, nil
	}

	return fc, nil
}

func (p *Parser) parseCast() (*ast.CastExpr, error) {
	startTok := p.current
	p.advance() // consume CAST
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	expr, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.AS); err != nil {
		return nil, err
	}
	typeName, err := p.parseCastTypeName()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return &ast.CastExpr{Pos: pos(startTok), Expr: expr, TypeName: typeName}, nil
}

func (p *Parser) parseCastTypeName() (string, error) {
	switch p.current.Type {
	case lexer.INT, lexer.INTEGER:
		p.advance()
		return "INT", nil
	case lexer.FLOAT:
		p.advance()
		return "FLOAT", nil
	case lexer.TEXT, lexer.VARCHAR:
		p.advance()
		return "TEXT", nil
	case lexer.BOOL, lexer.BOOLEAN:
		p.advance()
		return "BOOL", nil
	case lexer.IDENT:
		name := strings.ToUpper(p.current.Literal)
		p.advance()
		return name, nil
	default:
		return "", parseErrorf(p.current.Line, p.current.Col,
			"expected type name in CAST, got %s", p.current.Literal)
	}
}

func (p *Parser) parseCase() (*ast.CaseExpr, error) {
	startTok := p.current
	p.advance() // consume CASE

	ce := &ast.CaseExpr{Pos: pos(startTok)}

	// Optional operand (simple CASE)
	if !p.check(lexer.WHEN) {
		operand, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		ce.Operand = operand
	}

	// WHEN ... THEN ... pairs
	for p.check(lexer.WHEN) {
		p.advance()
		cond, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.THEN); err != nil {
			return nil, err
		}
		result, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		ce.Whens = append(ce.Whens, ast.WhenClause{Condition: cond, Result: result})
	}

	// Optional ELSE
	if p.check(lexer.ELSE) {
		p.advance()
		elseExpr, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		ce.ElseExpr = elseExpr
	}

	if _, err := p.expect(lexer.END); err != nil {
		return nil, err
	}

	return ce, nil
}

func (p *Parser) parseIn(left ast.Expression, opTok lexer.Token, negated bool) (*ast.InExpr, error) {
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}

	ie := &ast.InExpr{Pos: pos(opTok), Expr: left, Negated: negated}

	// Subquery form
	if p.check(lexer.SELECT) {
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		ie.Subquery = sub
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return ie, nil
	}

	// List form
	items, err := p.parseExpressionList()
	if err != nil {
		return nil, err
	}
	ie.List = items
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ie, nil
}

func (p *Parser) parseBetween(left ast.Expression, opTok lexer.Token, negated bool) (*ast.BetweenExpr, error) {
	low, err := p.parseExpression(5) // use addition precedence to avoid consuming AND
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.AND); err != nil {
		return nil, fmt.Errorf("expected AND in BETWEEN expression: %w", err)
	}
	high, err := p.parseExpression(5)
	if err != nil {
		return nil, err
	}
	return &ast.BetweenExpr{
		Pos:     pos(opTok),
		Expr:    left,
		Low:     low,
		High:    high,
		Negated: negated,
	}, nil
}

func (p *Parser) parseIsNull(left ast.Expression, opTok lexer.Token) (*ast.IsNullExpr, error) {
	negated := false
	if p.check(lexer.NOT) {
		negated = true
		p.advance()
	}
	if _, err := p.expect(lexer.NULL); err != nil {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected NULL after IS [NOT], got %s", p.current.Literal)
	}
	return &ast.IsNullExpr{Pos: pos(opTok), Expr: left, Negated: negated}, nil
}

// -----------------------------------------------------------------------
// CREATE TABLE
// -----------------------------------------------------------------------

func (p *Parser) parseCreateTable() (*ast.CreateTableStatement, error) {
	startTok := p.current
	if _, err := p.expect(lexer.CREATE); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TABLE); err != nil {
		return nil, err
	}

	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name, got %s", p.current.Literal)
	}
	name := p.current.Literal
	p.advance()

	// CREATE TABLE ... AS SELECT ...
	if p.current.Type == lexer.AS {
		p.advance() // consume AS
		sel, err := p.parseSelectOrSetOp()
		if err != nil {
			return nil, err
		}
		selStmt, ok := sel.(*ast.SelectStatement)
		if !ok {
			return nil, parseErrorf(p.current.Line, p.current.Col, "CREATE TABLE AS: expected SELECT")
		}
		return &ast.CreateTableStatement{
			Pos:        pos(startTok),
			Name:       name,
			SelectStmt: selStmt,
		}, nil
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}

	var cols []*ast.ColumnDef
	for {
		if p.check(lexer.RPAREN) {
			break
		}
		colDef, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		cols = append(cols, colDef)
		if !p.check(lexer.COMMA) {
			break
		}
		p.advance()
	}

	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	return &ast.CreateTableStatement{
		Pos:     pos(startTok),
		Name:    name,
		Columns: cols,
	}, nil
}

func (p *Parser) parseColumnDef() (*ast.ColumnDef, error) {
	colTok := p.current
	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected column name, got %s", p.current.Literal)
	}
	name := p.current.Literal
	p.advance()

	// Type
	typeName, err := p.parseTypeName()
	if err != nil {
		return nil, err
	}

	col := &ast.ColumnDef{Pos: pos(colTok), Name: name, TypeName: typeName}

	// Constraints
	for {
		if p.check(lexer.NOT) && p.peek.Type == lexer.NULL {
			col.NotNull = true
			p.advance()
			p.advance()
		} else if p.check(lexer.PRIMARY) && p.peek.Type == lexer.KEY {
			col.PrimaryKey = true
			col.NotNull = true
			p.advance()
			p.advance()
		} else if p.current.Type == lexer.DEFAULT {
			p.advance() // consume DEFAULT
			defaultExpr, err := p.parseUnary()
			if err != nil {
				return nil, fmt.Errorf("DEFAULT: %w", err)
			}
			col.DefaultVal = defaultExpr
		} else {
			break
		}
	}

	return col, nil
}

func (p *Parser) parseTypeName() (string, error) {
	switch p.current.Type {
	case lexer.INT, lexer.INTEGER:
		t := strings.ToUpper(p.current.Literal)
		p.advance()
		return t, nil
	case lexer.FLOAT:
		p.advance()
		return "FLOAT", nil
	case lexer.TEXT:
		p.advance()
		return "TEXT", nil
	case lexer.BOOL, lexer.BOOLEAN:
		p.advance()
		return "BOOL", nil
	case lexer.VARCHAR:
		p.advance()
		// Optional (n)
		if p.check(lexer.LPAREN) {
			p.advance()
			if !p.check(lexer.INT_LIT) {
				return "", parseErrorf(p.current.Line, p.current.Col, "expected integer in VARCHAR(n)")
			}
			n := p.current.Literal
			p.advance()
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return "", err
			}
			return fmt.Sprintf("VARCHAR(%s)", n), nil
		}
		return "VARCHAR", nil
	default:
		return "", parseErrorf(p.current.Line, p.current.Col,
			"expected type name (INT, FLOAT, TEXT, BOOL, VARCHAR), got %s", p.current.Literal)
	}
}

// -----------------------------------------------------------------------
// INSERT
// -----------------------------------------------------------------------

func (p *Parser) parseInsert() (*ast.InsertStatement, error) {
	startTok := p.current
	if _, err := p.expect(lexer.INSERT); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.INTO); err != nil {
		return nil, err
	}

	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name after INSERT INTO, got %s", p.current.Literal)
	}
	table := p.current.Literal
	p.advance()

	// Optional column list: only present when '(' follows the table name but
	// is NOT followed by a literal/expression (which would be a VALUES row).
	// Heuristic: if next is '(' and the token after that is an IDENT/keyword,
	// treat it as a column list; otherwise it's a positional insert (VALUES follows directly).
	var cols []string
	if p.check(lexer.LPAREN) && (p.peek.Type == lexer.IDENT || isKeyword(p.peek.Type)) {
		p.advance() // consume LPAREN
		for {
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected column name, got %s", p.current.Literal)
			}
			cols = append(cols, p.current.Literal)
			p.advance()
			if !p.check(lexer.COMMA) {
				break
			}
			p.advance()
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
	}

	// INSERT ... SELECT ...
	if p.check(lexer.SELECT) {
		sel, err := p.parseSelectOrSetOp()
		if err != nil {
			return nil, err
		}
		selStmt, ok := sel.(*ast.SelectStatement)
		if !ok {
			// For simplicity require a plain SELECT (not set-op) for INSERT SELECT
			return nil, parseErrorf(p.current.Line, p.current.Col, "INSERT ... SELECT: expected SELECT statement")
		}
		return &ast.InsertStatement{
			Pos:       pos(startTok),
			Table:     table,
			Columns:   cols,
			SelectStmt: selStmt,
		}, nil
	}

	if _, err := p.expect(lexer.VALUES); err != nil {
		return nil, err
	}

	// Parse one or more value rows: (v1, v2, ...) [, (v1, v2, ...)]
	var valueRows [][]ast.Expression
	for {
		if _, err := p.expect(lexer.LPAREN); err != nil {
			return nil, err
		}
		row, err := p.parseExpressionList()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		valueRows = append(valueRows, row)
		if !p.check(lexer.COMMA) {
			break
		}
		p.advance() // consume comma between rows
	}

	return &ast.InsertStatement{
		Pos:       pos(startTok),
		Table:     table,
		Columns:   cols,
		Values:    valueRows[0],
		ValueRows: valueRows,
	}, nil
}

// -----------------------------------------------------------------------
// UPDATE
// -----------------------------------------------------------------------

func (p *Parser) parseUpdate() (*ast.UpdateStatement, error) {
	startTok := p.current
	if _, err := p.expect(lexer.UPDATE); err != nil {
		return nil, err
	}

	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name after UPDATE, got %s", p.current.Literal)
	}
	table := p.current.Literal
	p.advance()

	if _, err := p.expect(lexer.SET); err != nil {
		return nil, err
	}

	var assigns []ast.UpdateAssign
	for {
		if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
			return nil, parseErrorf(p.current.Line, p.current.Col,
				"expected column name in SET clause, got %s", p.current.Literal)
		}
		col := p.current.Literal
		p.advance()

		if _, err := p.expect(lexer.EQ); err != nil {
			return nil, err
		}

		val, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		assigns = append(assigns, ast.UpdateAssign{Column: col, Value: val})

		if !p.check(lexer.COMMA) {
			break
		}
		p.advance()
	}

	stmt := &ast.UpdateStatement{
		Pos:     pos(startTok),
		Table:   table,
		Assigns: assigns,
	}

	if p.check(lexer.WHERE) {
		p.advance()
		where, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}

	return stmt, nil
}

// -----------------------------------------------------------------------
// DELETE
// -----------------------------------------------------------------------

func (p *Parser) parseDelete() (*ast.DeleteStatement, error) {
	startTok := p.current
	if _, err := p.expect(lexer.DELETE); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.FROM); err != nil {
		return nil, err
	}

	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name after DELETE FROM, got %s", p.current.Literal)
	}
	table := p.current.Literal
	p.advance()

	stmt := &ast.DeleteStatement{
		Pos:   pos(startTok),
		Table: table,
	}

	if p.check(lexer.WHERE) {
		p.advance()
		where, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}

	return stmt, nil
}

// -----------------------------------------------------------------------
// WITH (CTE)
// -----------------------------------------------------------------------

func (p *Parser) parseWithClause() (ast.Statement, error) {
	p.advance() // consume WITH

	// Optional RECURSIVE keyword
	recursive := false
	if p.current.Type == lexer.RECURSIVE {
		recursive = true
		p.advance()
	}

	var ctes []ast.CTEDef
	for {
		if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
			return nil, parseErrorf(p.current.Line, p.current.Col,
				"expected CTE name after WITH, got %s", p.current.Literal)
		}
		name := p.current.Literal
		p.advance()

		if _, err := p.expect(lexer.AS); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.LPAREN); err != nil {
			return nil, err
		}
		var def ast.CTEDef
		def.Name = name
		def.Recursive = recursive

		if recursive {
			// Recursive CTE: parse anchor SELECT, then optional UNION [ALL] recursive term.
			baseSel, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			def.Select = baseSel
			if p.current.Type == lexer.UNION {
				p.advance()
				unionAll := false
				if p.current.Type == lexer.ALL {
					unionAll = true
					p.advance()
				}
				recSel, err := p.parseSelect()
				if err != nil {
					return nil, err
				}
				def.RecursiveSelect = recSel
				def.RecursiveAll = unionAll
			}
		} else {
			// Non-recursive CTE: parse the full body as a SELECT or set-op (UNION/INTERSECT/EXCEPT).
			bodyStmt, err := p.parseSelectOrSetOp()
			if err != nil {
				return nil, err
			}
			if sel, ok := bodyStmt.(*ast.SelectStatement); ok {
				def.Select = sel
			} else {
				// Set-op body (e.g. SELECT 1 UNION SELECT 2)
				def.BodyStmt = bodyStmt
			}
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		ctes = append(ctes, def)

		if !p.check(lexer.COMMA) {
			break
		}
		p.advance()
	}

	// Now parse the main SELECT (or set-op)
	sel, err := p.parseSelectOrSetOp()
	if err != nil {
		return nil, err
	}

	// Attach CTEs to the outermost SELECT
	switch s := sel.(type) {
	case *ast.SelectStatement:
		s.CTEs = ctes
	case *ast.SetOpStatement:
		if inner, ok := s.Left.(*ast.SelectStatement); ok {
			inner.CTEs = ctes
		}
	}

	return sel, nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// isKeyword returns true for any keyword token type.
func isKeyword(tt lexer.TokenType) bool {
	_, ok := reverseKeywords[tt]
	return ok
}

// isReservedExprKeyword returns true for keywords that cannot start a primary expression.
func isReservedExprKeyword(tt lexer.TokenType) bool {
	switch tt {
	case lexer.FROM, lexer.WHERE, lexer.GROUP, lexer.ORDER, lexer.HAVING,
		lexer.LIMIT, lexer.OFFSET, lexer.JOIN, lexer.INNER, lexer.LEFT,
		lexer.RIGHT, lexer.CROSS, lexer.ON, lexer.AND, lexer.OR, lexer.NOT,
		lexer.IN, lexer.BETWEEN, lexer.LIKE, lexer.IS, lexer.ASC, lexer.DESC,
		lexer.THEN, lexer.ELSE, lexer.END, lexer.WHEN, lexer.UNION,
		lexer.INTERSECT, lexer.EXCEPT, lexer.SEMI, lexer.EOF:
		return true
	}
	return false
}

// reverseKeywords is a set of all keyword token types (used to check isKeyword).
var reverseKeywords = map[lexer.TokenType]string{
	lexer.SELECT: "SELECT", lexer.FROM: "FROM", lexer.WHERE: "WHERE",
	lexer.JOIN: "JOIN", lexer.ON: "ON", lexer.GROUP: "GROUP", lexer.BY: "BY",
	lexer.HAVING: "HAVING", lexer.ORDER: "ORDER", lexer.LIMIT: "LIMIT",
	lexer.OFFSET: "OFFSET", lexer.INSERT: "INSERT", lexer.INTO: "INTO",
	lexer.VALUES: "VALUES", lexer.CREATE: "CREATE", lexer.TABLE: "TABLE",
	lexer.AS: "AS", lexer.AND: "AND", lexer.OR: "OR", lexer.NOT: "NOT",
	lexer.IN: "IN", lexer.BETWEEN: "BETWEEN", lexer.LIKE: "LIKE",
	lexer.IS: "IS", lexer.NULL: "NULL", lexer.TRUE: "TRUE", lexer.FALSE: "FALSE",
	lexer.INNER: "INNER", lexer.LEFT: "LEFT", lexer.RIGHT: "RIGHT",
	lexer.CROSS: "CROSS", lexer.DISTINCT: "DISTINCT", lexer.ALL: "ALL",
	lexer.ASC: "ASC", lexer.DESC: "DESC", lexer.CASE: "CASE",
	lexer.WHEN: "WHEN", lexer.THEN: "THEN", lexer.ELSE: "ELSE", lexer.END: "END",
	lexer.EXISTS: "EXISTS", lexer.WITH: "WITH", lexer.UNION: "UNION",
	lexer.INTERSECT: "INTERSECT", lexer.EXCEPT: "EXCEPT",
	lexer.PRIMARY: "PRIMARY", lexer.KEY: "KEY", lexer.UNIQUE: "UNIQUE",
	lexer.DEFAULT: "DEFAULT", lexer.REFERENCES: "REFERENCES",
	lexer.INT: "INT", lexer.INTEGER: "INTEGER", lexer.FLOAT: "FLOAT",
	lexer.TEXT: "TEXT", lexer.VARCHAR: "VARCHAR", lexer.BOOL: "BOOL",
	lexer.BOOLEAN: "BOOLEAN",
	lexer.UPDATE: "UPDATE", lexer.SET: "SET", lexer.DELETE: "DELETE",
	lexer.FULL: "FULL", lexer.OUTER: "OUTER", lexer.CAST: "CAST",
	lexer.NULLS: "NULLS", lexer.FIRST: "FIRST", lexer.LAST: "LAST",
	lexer.EXPLAIN: "EXPLAIN", lexer.ANALYZE: "ANALYZE",
	lexer.DROP: "DROP", lexer.ALTER: "ALTER", lexer.ADD: "ADD",
	lexer.COLUMN: "COLUMN", lexer.RENAME: "RENAME",
	lexer.NATURAL: "NATURAL", lexer.USING: "USING", lexer.RECURSIVE: "RECURSIVE",
	lexer.OVER: "OVER", lexer.PARTITION: "PARTITION",
	lexer.ROWS: "ROWS", lexer.RANGE: "RANGE",
	lexer.UNBOUNDED: "UNBOUNDED", lexer.PRECEDING: "PRECEDING", lexer.FOLLOWING: "FOLLOWING",
	lexer.EXTRACT: "EXTRACT", lexer.INTERVAL: "INTERVAL",
}

// -----------------------------------------------------------------------
// New statement parsers
// -----------------------------------------------------------------------

// parseExplain parses EXPLAIN [ANALYZE] <statement>.
func (p *Parser) parseExplain() (*ast.ExplainStatement, error) {
	startTok := p.current
	p.advance() // consume EXPLAIN
	analyze := false
	if p.current.Type == lexer.ANALYZE {
		analyze = true
		p.advance()
	}
	inner, err := p.ParseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.ExplainStatement{Pos: pos(startTok), Analyze: analyze, Stmt: inner}, nil
}

// parseDropTable parses DROP TABLE [IF EXISTS] name.
func (p *Parser) parseDropTable() (*ast.DropTableStatement, error) {
	startTok := p.current
	p.advance() // consume DROP
	if _, err := p.expect(lexer.TABLE); err != nil {
		return nil, err
	}
	ifExists := false
	if p.current.Type == lexer.IDENT && strings.ToUpper(p.current.Literal) == "IF" {
		p.advance()
		if p.current.Type == lexer.EXISTS {
			p.advance()
			ifExists = true
		}
	}
	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name after DROP TABLE, got %s", p.current.Literal)
	}
	name := p.current.Literal
	p.advance()
	return &ast.DropTableStatement{Pos: pos(startTok), Name: name, IfExists: ifExists}, nil
}

// parseAlterTable parses ALTER TABLE name ADD [COLUMN] col_def
//
//	| DROP [COLUMN] col_name
//	| RENAME TO new_name.
func (p *Parser) parseAlterTable() (*ast.AlterTableStatement, error) {
	startTok := p.current
	p.advance() // consume ALTER
	if _, err := p.expect(lexer.TABLE); err != nil {
		return nil, err
	}
	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected table name after ALTER TABLE, got %s", p.current.Literal)
	}
	tableName := p.current.Literal
	p.advance()

	stmt := &ast.AlterTableStatement{Pos: pos(startTok), Table: tableName}

	switch {
	case p.current.Type == lexer.ADD:
		stmt.Action = "ADD"
		p.advance()
		if p.current.Type == lexer.COLUMN {
			p.advance() // optional COLUMN keyword
		}
		colDef, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		stmt.ColDef = colDef

	case p.current.Type == lexer.DROP:
		stmt.Action = "DROP"
		p.advance()
		if p.current.Type == lexer.COLUMN {
			p.advance() // optional COLUMN keyword
		}
		if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
			return nil, parseErrorf(p.current.Line, p.current.Col,
				"expected column name after DROP COLUMN, got %s", p.current.Literal)
		}
		stmt.ColName = p.current.Literal
		p.advance()

	case p.current.Type == lexer.RENAME:
		p.advance() // consume RENAME
		if p.current.Type == lexer.COLUMN {
			// RENAME COLUMN old_name TO new_name
			stmt.Action = "RENAME_COLUMN"
			p.advance() // consume COLUMN
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected old column name after RENAME COLUMN, got %s", p.current.Literal)
			}
			stmt.ColName = p.current.Literal
			p.advance()
			// Optional TO keyword
			if p.current.Type == lexer.IDENT && strings.ToUpper(p.current.Literal) == "TO" {
				p.advance()
			}
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected new column name, got %s", p.current.Literal)
			}
			stmt.NewName = p.current.Literal
			p.advance()
		} else {
			// RENAME [TO] new_table_name
			stmt.Action = "RENAME"
			if p.current.Type == lexer.IDENT && strings.ToUpper(p.current.Literal) == "TO" {
				p.advance()
			}
			if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
				return nil, parseErrorf(p.current.Line, p.current.Col,
					"expected new table name after RENAME TO, got %s", p.current.Literal)
			}
			stmt.NewName = p.current.Literal
			p.advance()
		}

	default:
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected ADD, DROP, or RENAME after ALTER TABLE name, got %s", p.current.Literal)
	}

	return stmt, nil
}

// parseExtract parses EXTRACT(part FROM expr).
func (p *Parser) parseExtract() (*ast.ExtractExpr, error) {
	startTok := p.current
	p.advance() // consume EXTRACT
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	// date part: YEAR, MONTH, DAY, HOUR, MINUTE, SECOND, DOW, DOY, EPOCH, QUARTER, WEEK
	if !p.check(lexer.IDENT) && !isKeyword(p.current.Type) {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected date part in EXTRACT, got %s", p.current.Literal)
	}
	part := strings.ToUpper(p.current.Literal)
	p.advance()
	// expect FROM
	if p.current.Type != lexer.FROM {
		return nil, parseErrorf(p.current.Line, p.current.Col,
			"expected FROM in EXTRACT, got %s", p.current.Literal)
	}
	p.advance()
	fromExpr, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return &ast.ExtractExpr{Pos: pos(startTok), Part: part, From: fromExpr}, nil
}

// parseWindowSpec parses ( [PARTITION BY ...] [ORDER BY ...] [frame] ).
func (p *Parser) parseWindowSpec() (*ast.WindowSpec, error) {
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	spec := &ast.WindowSpec{}

	// PARTITION BY
	if p.current.Type == lexer.PARTITION {
		p.advance()
		if _, err := p.expect(lexer.BY); err != nil {
			return nil, err
		}
		parts, err := p.parseExpressionList()
		if err != nil {
			return nil, err
		}
		spec.PartitionBy = parts
	}

	// ORDER BY
	if p.current.Type == lexer.ORDER {
		p.advance()
		if _, err := p.expect(lexer.BY); err != nil {
			return nil, err
		}
		sorts, err := p.parseSortSpecs()
		if err != nil {
			return nil, err
		}
		spec.OrderBy = sorts
	}

	// Frame clause: ROWS/RANGE BETWEEN ... AND ...
	if p.current.Type == lexer.ROWS || p.current.Type == lexer.RANGE {
		frame, err := p.parseWindowFrame()
		if err != nil {
			return nil, err
		}
		spec.Frame = frame
	}

	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return spec, nil
}

// parseWindowFrame parses ROWS|RANGE BETWEEN bound AND bound.
func (p *Parser) parseWindowFrame() (*ast.WindowFrame, error) {
	mode := strings.ToUpper(p.current.Literal)
	p.advance() // consume ROWS or RANGE

	frame := &ast.WindowFrame{Mode: mode}

	// BETWEEN start AND end, or just start (implicit end = CURRENT ROW)
	if p.current.Type == lexer.BETWEEN {
		p.advance()
		start, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		frame.Start = start
		if _, err := p.expect(lexer.AND); err != nil {
			return nil, err
		}
		end, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		frame.End = end
	} else {
		start, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		frame.Start = start
		frame.End = ast.FrameBound{Kind: ast.FrameCurrentRow}
	}
	return frame, nil
}

// parseFrameBound parses UNBOUNDED PRECEDING | N PRECEDING | CURRENT ROW | N FOLLOWING | UNBOUNDED FOLLOWING.
func (p *Parser) parseFrameBound() (ast.FrameBound, error) {
	tok := p.current
	upper := strings.ToUpper(tok.Literal)

	if p.current.Type == lexer.UNBOUNDED {
		p.advance()
		nextUpper := strings.ToUpper(p.current.Literal)
		if p.current.Type == lexer.PRECEDING || nextUpper == "PRECEDING" {
			p.advance()
			return ast.FrameBound{Kind: ast.FrameUnboundedPreceding}, nil
		}
		if p.current.Type == lexer.FOLLOWING || nextUpper == "FOLLOWING" {
			p.advance()
			return ast.FrameBound{Kind: ast.FrameUnboundedFollowing}, nil
		}
		return ast.FrameBound{}, parseErrorf(tok.Line, tok.Col, "expected PRECEDING or FOLLOWING after UNBOUNDED")
	}

	if upper == "CURRENT" {
		p.advance()
		if strings.ToUpper(p.current.Literal) == "ROW" {
			p.advance()
		}
		return ast.FrameBound{Kind: ast.FrameCurrentRow}, nil
	}

	// N PRECEDING or N FOLLOWING
	offset, err := p.parseExpression(0)
	if err != nil {
		return ast.FrameBound{}, err
	}
	nextUpper := strings.ToUpper(p.current.Literal)
	if p.current.Type == lexer.PRECEDING || nextUpper == "PRECEDING" {
		p.advance()
		return ast.FrameBound{Kind: ast.FrameNPreceding, Offset: offset}, nil
	}
	if p.current.Type == lexer.FOLLOWING || nextUpper == "FOLLOWING" {
		p.advance()
		return ast.FrameBound{Kind: ast.FrameNFollowing, Offset: offset}, nil
	}
	return ast.FrameBound{}, parseErrorf(p.current.Line, p.current.Col,
		"expected PRECEDING or FOLLOWING in frame bound, got %s", p.current.Literal)
}
