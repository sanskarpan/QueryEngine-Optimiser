// Package parser implements a recursive-descent SQL parser that converts a token stream
// produced by the lexer into AST nodes. It supports SELECT, INSERT, UPDATE, DELETE,
// CREATE/DROP/ALTER TABLE, EXPLAIN, CTEs, subqueries, and window functions.
package parser

import "fmt"

// ParseError is returned when the parser encounters a syntax error.
type ParseError struct {
	Message string
	Line    int
	Col     int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at line %d, col %d: %s", e.Line, e.Col, e.Message)
}

func parseErrorf(line, col int, format string, args ...interface{}) *ParseError {
	return &ParseError{
		Message: fmt.Sprintf(format, args...),
		Line:    line,
		Col:     col,
	}
}
