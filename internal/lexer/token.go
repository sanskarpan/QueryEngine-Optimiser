// Package lexer implements the SQL lexer (tokenizer) for the query engine.
// It converts a raw SQL string into a stream of typed tokens consumed by the parser.
package lexer

// TokenType identifies the type of a lexical token.
type TokenType int

const (
	// Meta
	EOF     TokenType = iota
	ILLEGAL           // unrecognized character

	// Identifiers and literals
	IDENT      // table_name, column, etc.
	INT_LIT    // 42
	FLOAT_LIT  // 3.14
	STRING_LIT // 'hello'
	BOOL_LIT   // TRUE / FALSE (also keywords, resolved at lex time)

	// Keywords
	SELECT
	FROM
	WHERE
	JOIN
	ON
	GROUP
	BY
	HAVING
	ORDER
	LIMIT
	OFFSET
	INSERT
	INTO
	VALUES
	CREATE
	TABLE
	AS
	AND
	OR
	NOT
	IN
	BETWEEN
	LIKE
	IS
	NULL
	TRUE
	FALSE
	INNER
	LEFT
	RIGHT
	CROSS
	DISTINCT
	ALL
	ASC
	DESC
	CASE
	WHEN
	THEN
	ELSE
	END
	EXISTS
	WITH
	UNION
	INTERSECT
	EXCEPT
	PRIMARY
	KEY
	UNIQUE
	DEFAULT
	REFERENCES
	INT
	INTEGER
	FLOAT
	TEXT
	VARCHAR
	BOOL
	BOOLEAN
	// DML keywords
	UPDATE
	SET
	DELETE
	// JOIN extensions
	FULL
	OUTER
	// Type conversion
	CAST
	// NULL ordering
	NULLS
	FIRST
	LAST
	// EXPLAIN
	EXPLAIN
	ANALYZE
	// DDL extensions
	DROP
	ALTER
	ADD
	COLUMN
	RENAME
	// JOIN extensions
	NATURAL
	USING
	// CTE extension
	RECURSIVE
	// Window functions
	OVER
	PARTITION
	ROWS
	RANGE
	UNBOUNDED
	PRECEDING
	FOLLOWING
	// LIKE escape
	ESCAPE
	// Date/time
	EXTRACT
	INTERVAL

	// Operators
	EQ     // =
	NEQ    // != or <>
	LT     // <
	GT     // >
	LTE    // <=
	GTE    // >=
	PLUS   // +
	MINUS  // -
	STAR   // *
	SLASH  // /
	PERCENT // %
	CONCAT // ||

	// Symbols
	LPAREN // (
	RPAREN // )
	COMMA  // ,
	DOT    // .
	SEMI   // ;
)

// keywords maps uppercase keyword strings to their TokenType.
var keywords = map[string]TokenType{
	"SELECT":     SELECT,
	"FROM":       FROM,
	"WHERE":      WHERE,
	"JOIN":       JOIN,
	"ON":         ON,
	"GROUP":      GROUP,
	"BY":         BY,
	"HAVING":     HAVING,
	"ORDER":      ORDER,
	"LIMIT":      LIMIT,
	"OFFSET":     OFFSET,
	"INSERT":     INSERT,
	"INTO":       INTO,
	"VALUES":     VALUES,
	"CREATE":     CREATE,
	"TABLE":      TABLE,
	"AS":         AS,
	"AND":        AND,
	"OR":         OR,
	"NOT":        NOT,
	"IN":         IN,
	"BETWEEN":    BETWEEN,
	"LIKE":       LIKE,
	"IS":         IS,
	"NULL":       NULL,
	"TRUE":       TRUE,
	"FALSE":      FALSE,
	"INNER":      INNER,
	"LEFT":       LEFT,
	"RIGHT":      RIGHT,
	"CROSS":      CROSS,
	"DISTINCT":   DISTINCT,
	"ALL":        ALL,
	"ASC":        ASC,
	"DESC":       DESC,
	"CASE":       CASE,
	"WHEN":       WHEN,
	"THEN":       THEN,
	"ELSE":       ELSE,
	"END":        END,
	"EXISTS":     EXISTS,
	"WITH":       WITH,
	"UNION":      UNION,
	"INTERSECT":  INTERSECT,
	"EXCEPT":     EXCEPT,
	"PRIMARY":    PRIMARY,
	"KEY":        KEY,
	"UNIQUE":     UNIQUE,
	"DEFAULT":    DEFAULT,
	"REFERENCES": REFERENCES,
	"INT":        INT,
	"INTEGER":    INTEGER,
	"FLOAT":      FLOAT,
	"TEXT":       TEXT,
	"VARCHAR":    VARCHAR,
	"BOOL":       BOOL,
	"BOOLEAN":    BOOLEAN,
	"UPDATE":     UPDATE,
	"SET":        SET,
	"DELETE":     DELETE,
	"FULL":       FULL,
	"OUTER":      OUTER,
	"CAST":       CAST,
	"NULLS":      NULLS,
	"FIRST":      FIRST,
	"LAST":       LAST,
	"EXPLAIN":    EXPLAIN,
	"ANALYZE":    ANALYZE,
	"DROP":       DROP,
	"ALTER":      ALTER,
	"ADD":        ADD,
	"COLUMN":     COLUMN,
	"RENAME":     RENAME,
	"NATURAL":    NATURAL,
	"USING":      USING,
	"RECURSIVE":  RECURSIVE,
	"OVER":       OVER,
	"PARTITION":  PARTITION,
	"ROWS":       ROWS,
	"RANGE":      RANGE,
	"UNBOUNDED":  UNBOUNDED,
	"PRECEDING":  PRECEDING,
	"FOLLOWING":  FOLLOWING,
	"EXTRACT":    EXTRACT,
	"INTERVAL":   INTERVAL,
	"ESCAPE":     ESCAPE,
}

// Token is a single lexical unit.
type Token struct {
	Type    TokenType
	Literal string // raw text as it appeared in input
	Line    int
	Col     int
}

// tokenNames maps TokenType to a human-readable name for debugging.
var tokenNames = map[TokenType]string{
	EOF:     "EOF",
	ILLEGAL: "ILLEGAL",
	IDENT:   "IDENT",
	INT_LIT: "INT_LIT",
	FLOAT_LIT: "FLOAT_LIT",
	STRING_LIT: "STRING_LIT",
	BOOL_LIT:  "BOOL_LIT",
	SELECT: "SELECT", FROM: "FROM", WHERE: "WHERE", JOIN: "JOIN",
	ON: "ON", GROUP: "GROUP", BY: "BY", HAVING: "HAVING",
	ORDER: "ORDER", LIMIT: "LIMIT", OFFSET: "OFFSET",
	INSERT: "INSERT", INTO: "INTO", VALUES: "VALUES",
	CREATE: "CREATE", TABLE: "TABLE", AS: "AS",
	AND: "AND", OR: "OR", NOT: "NOT",
	IN: "IN", BETWEEN: "BETWEEN", LIKE: "LIKE", IS: "IS",
	NULL: "NULL", TRUE: "TRUE", FALSE: "FALSE",
	INNER: "INNER", LEFT: "LEFT", RIGHT: "RIGHT", CROSS: "CROSS",
	DISTINCT: "DISTINCT", ALL: "ALL", ASC: "ASC", DESC: "DESC",
	CASE: "CASE", WHEN: "WHEN", THEN: "THEN", ELSE: "ELSE", END: "END",
	EXISTS: "EXISTS", WITH: "WITH", UNION: "UNION",
	INTERSECT: "INTERSECT", EXCEPT: "EXCEPT",
	PRIMARY: "PRIMARY", KEY: "KEY", UNIQUE: "UNIQUE",
	DEFAULT: "DEFAULT", REFERENCES: "REFERENCES",
	INT: "INT", INTEGER: "INTEGER", FLOAT: "FLOAT", TEXT: "TEXT",
	VARCHAR: "VARCHAR", BOOL: "BOOL", BOOLEAN: "BOOLEAN",
	UPDATE: "UPDATE", SET: "SET", DELETE: "DELETE",
	FULL: "FULL", OUTER: "OUTER", CAST: "CAST",
	NULLS: "NULLS", FIRST: "FIRST", LAST: "LAST",
	EXPLAIN: "EXPLAIN", ANALYZE: "ANALYZE",
	DROP: "DROP", ALTER: "ALTER", ADD: "ADD", COLUMN: "COLUMN", RENAME: "RENAME",
	NATURAL: "NATURAL", USING: "USING", RECURSIVE: "RECURSIVE",
	OVER: "OVER", PARTITION: "PARTITION",
	ROWS: "ROWS", RANGE: "RANGE", UNBOUNDED: "UNBOUNDED", PRECEDING: "PRECEDING", FOLLOWING: "FOLLOWING",
	EXTRACT: "EXTRACT", INTERVAL: "INTERVAL",
	EQ: "=", NEQ: "!=", LT: "<", GT: ">", LTE: "<=", GTE: ">=",
	PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", PERCENT: "%", CONCAT: "||",
	LPAREN: "(", RPAREN: ")", COMMA: ",", DOT: ".", SEMI: ";",
}

// String returns a human-readable name for a TokenType.
func (t TokenType) String() string {
	if name, ok := tokenNames[t]; ok {
		return name
	}
	return "UNKNOWN"
}
