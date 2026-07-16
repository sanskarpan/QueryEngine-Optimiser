package lexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectAll tokenizes the input and returns all tokens until EOF.
func collectAll(input string) []Token {
	l := New(input)
	var tokens []Token
	for {
		tok := l.Next()
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}
	return tokens
}

func tokenTypes(tokens []Token) []TokenType {
	types := make([]TokenType, len(tokens))
	for i, t := range tokens {
		types[i] = t.Type
	}
	return types
}

func TestLexer_Keywords(t *testing.T) {
	input := "SELECT FROM WHERE JOIN ON GROUP BY HAVING ORDER LIMIT OFFSET INSERT INTO VALUES CREATE TABLE AS AND OR NOT IN BETWEEN LIKE IS NULL TRUE FALSE INNER LEFT RIGHT CROSS DISTINCT ALL ASC DESC CASE WHEN THEN ELSE END EXISTS"
	tokens := collectAll(input)
	expected := []TokenType{
		SELECT, FROM, WHERE, JOIN, ON, GROUP, BY, HAVING, ORDER, LIMIT, OFFSET,
		INSERT, INTO, VALUES, CREATE, TABLE, AS, AND, OR, NOT, IN, BETWEEN, LIKE,
		IS, NULL, TRUE, FALSE, INNER, LEFT, RIGHT, CROSS, DISTINCT, ALL, ASC, DESC,
		CASE, WHEN, THEN, ELSE, END, EXISTS, EOF,
	}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_KeywordsCaseInsensitive(t *testing.T) {
	tests := []struct {
		input    string
		expected TokenType
	}{
		{"select", SELECT},
		{"Select", SELECT},
		{"SELECT", SELECT},
		{"from", FROM},
		{"WHERE", WHERE},
		{"null", NULL},
		{"True", TRUE},
		{"false", FALSE},
	}
	for _, tt := range tests {
		l := New(tt.input)
		tok := l.Next()
		assert.Equal(t, tt.expected, tok.Type, "input: %q", tt.input)
	}
}

func TestLexer_Operators(t *testing.T) {
	input := "= != <> < > <= >= + - * / % ||"
	tokens := collectAll(input)
	expected := []TokenType{EQ, NEQ, NEQ, LT, GT, LTE, GTE, PLUS, MINUS, STAR, SLASH, PERCENT, CONCAT, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_Symbols(t *testing.T) {
	input := "( ) , . ;"
	tokens := collectAll(input)
	expected := []TokenType{LPAREN, RPAREN, COMMA, DOT, SEMI, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_StringLiteral(t *testing.T) {
	tests := []struct {
		input   string
		literal string
		tokType TokenType
	}{
		{"'hello'", "hello", STRING_LIT},
		{"'it''s fine'", "it's fine", STRING_LIT},
		{"''", "", STRING_LIT},
		{"'hello world'", "hello world", STRING_LIT},
	}
	for _, tt := range tests {
		l := New(tt.input)
		tok := l.Next()
		assert.Equal(t, tt.tokType, tok.Type, "input: %q", tt.input)
		assert.Equal(t, tt.literal, tok.Literal, "input: %q", tt.input)
	}
}

func TestLexer_UnterminatedString(t *testing.T) {
	l := New("'hello")
	tok := l.Next()
	assert.Equal(t, ILLEGAL, tok.Type)
}

func TestLexer_Numbers(t *testing.T) {
	tests := []struct {
		input   string
		tokType TokenType
		literal string
	}{
		{"42", INT_LIT, "42"},
		{"0", INT_LIT, "0"},
		{"3.14", FLOAT_LIT, "3.14"},
		{"0.5", FLOAT_LIT, "0.5"},
		{"100.0", FLOAT_LIT, "100.0"},
	}
	for _, tt := range tests {
		l := New(tt.input)
		tok := l.Next()
		assert.Equal(t, tt.tokType, tok.Type, "input: %q", tt.input)
		assert.Equal(t, tt.literal, tok.Literal, "input: %q", tt.input)
	}
}

func TestLexer_Identifiers(t *testing.T) {
	tests := []struct {
		input   string
		literal string
	}{
		{"foo", "foo"},
		{"table_name", "table_name"},
		{"_private", "_private"},
		{"col123", "col123"},
	}
	for _, tt := range tests {
		l := New(tt.input)
		tok := l.Next()
		assert.Equal(t, IDENT, tok.Type, "input: %q", tt.input)
		assert.Equal(t, tt.literal, tok.Literal, "input: %q", tt.input)
	}
}

func TestLexer_QuotedIdentifier(t *testing.T) {
	l := New(`"my table"`)
	tok := l.Next()
	assert.Equal(t, IDENT, tok.Type)
	assert.Equal(t, "my table", tok.Literal)
}

func TestLexer_Position(t *testing.T) {
	input := "SELECT\nFROM\nWHERE"
	l := New(input)

	tok1 := l.Next()
	assert.Equal(t, SELECT, tok1.Type)
	assert.Equal(t, 1, tok1.Line)
	assert.Equal(t, 1, tok1.Col)

	tok2 := l.Next()
	assert.Equal(t, FROM, tok2.Type)
	assert.Equal(t, 2, tok2.Line)
	assert.Equal(t, 1, tok2.Col)

	tok3 := l.Next()
	assert.Equal(t, WHERE, tok3.Type)
	assert.Equal(t, 3, tok3.Line)
	assert.Equal(t, 1, tok3.Col)
}

func TestLexer_MultilineColTracking(t *testing.T) {
	input := "SELECT id,\nname FROM t"
	l := New(input)

	tokens := collectAll(input)
	_ = tokens

	l = New(input)
	sel := l.Next()
	assert.Equal(t, 1, sel.Line)
	assert.Equal(t, 1, sel.Col)

	id := l.Next()
	assert.Equal(t, IDENT, id.Type)
	assert.Equal(t, 1, id.Line)
	assert.Equal(t, 8, id.Col)

	comma := l.Next()
	assert.Equal(t, COMMA, comma.Type)
	assert.Equal(t, 1, comma.Line)

	name := l.Next()
	assert.Equal(t, IDENT, name.Type)
	assert.Equal(t, 2, name.Line)
	assert.Equal(t, 1, name.Col)

	from := l.Next()
	assert.Equal(t, FROM, from.Type)
	assert.Equal(t, 2, from.Line)
}

func TestLexer_Comments_SingleLine(t *testing.T) {
	input := "SELECT -- this is a comment\nFROM"
	tokens := collectAll(input)
	expected := []TokenType{SELECT, FROM, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_Comments_MultiLine(t *testing.T) {
	input := "SELECT /* this is\na multiline comment */ FROM"
	tokens := collectAll(input)
	expected := []TokenType{SELECT, FROM, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_EmptyInput(t *testing.T) {
	l := New("")
	tok := l.Next()
	assert.Equal(t, EOF, tok.Type)
}

func TestLexer_WhitespaceOnly(t *testing.T) {
	l := New("   \t\n  ")
	tok := l.Next()
	assert.Equal(t, EOF, tok.Type)
}

func TestLexer_ILLEGAL(t *testing.T) {
	l := New("@")
	tok := l.Next()
	assert.Equal(t, ILLEGAL, tok.Type)
	assert.Equal(t, "@", tok.Literal)
}

func TestLexer_Peek(t *testing.T) {
	l := New("SELECT FROM")

	// Peek should not advance
	p1 := l.Peek()
	p2 := l.Peek()
	n1 := l.Next()

	assert.Equal(t, SELECT, p1.Type)
	assert.Equal(t, SELECT, p2.Type)
	assert.Equal(t, SELECT, n1.Type)

	// Now Next should return FROM
	n2 := l.Next()
	assert.Equal(t, FROM, n2.Type)
}

func TestLexer_FullSelect(t *testing.T) {
	input := "SELECT id, name FROM customers WHERE id = 1"
	tokens := collectAll(input)
	expected := []TokenType{
		SELECT, IDENT, COMMA, IDENT, FROM, IDENT, WHERE, IDENT, EQ, INT_LIT, EOF,
	}
	require.Equal(t, expected, tokenTypes(tokens))

	// Check literals
	assert.Equal(t, "id", tokens[1].Literal)
	assert.Equal(t, "name", tokens[3].Literal)
	assert.Equal(t, "customers", tokens[5].Literal)
	assert.Equal(t, "id", tokens[7].Literal)
	assert.Equal(t, "1", tokens[9].Literal)
}

func TestLexer_StarInSelect(t *testing.T) {
	input := "SELECT * FROM t"
	tokens := collectAll(input)
	expected := []TokenType{SELECT, STAR, FROM, IDENT, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}

func TestLexer_FloatWithNoLeadingDigit(t *testing.T) {
	// .5 should lex as DOT then INT_LIT (not FLOAT_LIT)
	// since we require digit before dot
	input := ".5"
	l := New(input)
	tok1 := l.Next()
	assert.Equal(t, DOT, tok1.Type)
	tok2 := l.Next()
	assert.Equal(t, INT_LIT, tok2.Type)
	assert.Equal(t, "5", tok2.Literal)
}

func TestLexer_TypeKeywords(t *testing.T) {
	input := "INT INTEGER FLOAT TEXT VARCHAR BOOL BOOLEAN"
	tokens := collectAll(input)
	expected := []TokenType{INT, INTEGER, FLOAT, TEXT, VARCHAR, BOOL, BOOLEAN, EOF}
	assert.Equal(t, expected, tokenTypes(tokens))
}
