package lexer

import (
	"strings"
	"unicode"
)

// Lexer converts a SQL string into a stream of tokens.
type Lexer struct {
	input []rune
	pos   int
	line  int
	col   int
}

// New creates a new Lexer for the given SQL input.
func New(input string) *Lexer {
	return &Lexer{
		input: []rune(input),
		pos:   0,
		line:  1,
		col:   1,
	}
}

// current returns the rune at pos without advancing.
func (l *Lexer) current() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

// peek1 returns the rune one ahead of pos without advancing.
func (l *Lexer) peek1() rune {
	if l.pos+1 >= len(l.input) {
		return 0
	}
	return l.input[l.pos+1]
}

// advance moves pos forward by one and updates line/col.
func (l *Lexer) advance() rune {
	ch := l.input[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

// Next returns the next token and advances the lexer.
func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.input) {
		return Token{Type: EOF, Literal: "", Line: l.line, Col: l.col}
	}

	startLine := l.line
	startCol := l.col
	ch := l.current()

	switch {
	case ch == '\'':
		return l.lexString(startLine, startCol)
	case ch == '"':
		return l.lexQuotedIdent(startLine, startCol)
	case unicode.IsDigit(ch):
		return l.lexNumber(startLine, startCol)
	case unicode.IsLetter(ch) || ch == '_':
		return l.lexIdentOrKeyword(startLine, startCol)
	default:
		return l.lexSymbolOrOperator(startLine, startCol)
	}
}

// Peek returns the next token WITHOUT advancing the lexer.
func (l *Lexer) Peek() Token {
	savedPos := l.pos
	savedLine := l.line
	savedCol := l.col

	tok := l.Next()

	l.pos = savedPos
	l.line = savedLine
	l.col = savedCol

	return tok
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.current()

		// Whitespace
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			l.advance()
			continue
		}

		// Single-line comment --
		if ch == '-' && l.peek1() == '-' {
			for l.pos < len(l.input) && l.current() != '\n' {
				l.advance()
			}
			continue
		}

		// Multi-line comment /* ... */
		if ch == '/' && l.peek1() == '*' {
			l.advance() // /
			l.advance() // *
			for l.pos < len(l.input) {
				if l.current() == '*' && l.peek1() == '/' {
					l.advance() // *
					l.advance() // /
					break
				}
				l.advance()
			}
			continue
		}

		break
	}
}

func (l *Lexer) lexString(line, col int) Token {
	l.advance() // consume opening '
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.current()
		if ch == '\'' {
			l.advance()
			// '' is an escaped single quote inside string
			if l.pos < len(l.input) && l.current() == '\'' {
				sb.WriteRune('\'')
				l.advance()
				continue
			}
			// end of string
			return Token{Type: STRING_LIT, Literal: sb.String(), Line: line, Col: col}
		}
		sb.WriteRune(l.advance())
	}
	// Unterminated string — return ILLEGAL
	return Token{Type: ILLEGAL, Literal: "unterminated string", Line: line, Col: col}
}

func (l *Lexer) lexQuotedIdent(line, col int) Token {
	l.advance() // consume opening "
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.current()
		if ch == '"' {
			l.advance()
			return Token{Type: IDENT, Literal: sb.String(), Line: line, Col: col}
		}
		sb.WriteRune(l.advance())
	}
	return Token{Type: ILLEGAL, Literal: "unterminated quoted identifier", Line: line, Col: col}
}

func (l *Lexer) lexNumber(line, col int) Token {
	var sb strings.Builder
	isFloat := false

	for l.pos < len(l.input) && unicode.IsDigit(l.current()) {
		sb.WriteRune(l.advance())
	}

	// Check for decimal point
	if l.pos < len(l.input) && l.current() == '.' && unicode.IsDigit(l.peek1()) {
		isFloat = true
		sb.WriteRune(l.advance()) // consume '.'
		for l.pos < len(l.input) && unicode.IsDigit(l.current()) {
			sb.WriteRune(l.advance())
		}
	}

	if isFloat {
		return Token{Type: FLOAT_LIT, Literal: sb.String(), Line: line, Col: col}
	}
	return Token{Type: INT_LIT, Literal: sb.String(), Line: line, Col: col}
}

func (l *Lexer) lexIdentOrKeyword(line, col int) Token {
	var sb strings.Builder
	for l.pos < len(l.input) && (unicode.IsLetter(l.current()) || unicode.IsDigit(l.current()) || l.current() == '_') {
		sb.WriteRune(l.advance())
	}

	literal := sb.String()
	upper := strings.ToUpper(literal)

	if tt, ok := keywords[upper]; ok {
		return Token{Type: tt, Literal: literal, Line: line, Col: col}
	}

	return Token{Type: IDENT, Literal: literal, Line: line, Col: col}
}

func (l *Lexer) lexSymbolOrOperator(line, col int) Token {
	ch := l.advance()

	switch ch {
	case '(':
		return Token{Type: LPAREN, Literal: "(", Line: line, Col: col}
	case ')':
		return Token{Type: RPAREN, Literal: ")", Line: line, Col: col}
	case ',':
		return Token{Type: COMMA, Literal: ",", Line: line, Col: col}
	case '.':
		return Token{Type: DOT, Literal: ".", Line: line, Col: col}
	case ';':
		return Token{Type: SEMI, Literal: ";", Line: line, Col: col}
	case '+':
		return Token{Type: PLUS, Literal: "+", Line: line, Col: col}
	case '-':
		return Token{Type: MINUS, Literal: "-", Line: line, Col: col}
	case '*':
		return Token{Type: STAR, Literal: "*", Line: line, Col: col}
	case '/':
		return Token{Type: SLASH, Literal: "/", Line: line, Col: col}
	case '%':
		return Token{Type: PERCENT, Literal: "%", Line: line, Col: col}
	case '|':
		if l.pos < len(l.input) && l.current() == '|' {
			l.advance()
			return Token{Type: CONCAT, Literal: "||", Line: line, Col: col}
		}
		return Token{Type: ILLEGAL, Literal: "|", Line: line, Col: col}
	case '=':
		return Token{Type: EQ, Literal: "=", Line: line, Col: col}
	case '!':
		if l.pos < len(l.input) && l.current() == '=' {
			l.advance()
			return Token{Type: NEQ, Literal: "!=", Line: line, Col: col}
		}
		return Token{Type: ILLEGAL, Literal: "!", Line: line, Col: col}
	case '<':
		if l.pos < len(l.input) && l.current() == '=' {
			l.advance()
			return Token{Type: LTE, Literal: "<=", Line: line, Col: col}
		}
		if l.pos < len(l.input) && l.current() == '>' {
			l.advance()
			return Token{Type: NEQ, Literal: "<>", Line: line, Col: col}
		}
		return Token{Type: LT, Literal: "<", Line: line, Col: col}
	case '>':
		if l.pos < len(l.input) && l.current() == '=' {
			l.advance()
			return Token{Type: GTE, Literal: ">=", Line: line, Col: col}
		}
		return Token{Type: GT, Literal: ">", Line: line, Col: col}
	default:
		return Token{Type: ILLEGAL, Literal: string(ch), Line: line, Col: col}
	}
}
