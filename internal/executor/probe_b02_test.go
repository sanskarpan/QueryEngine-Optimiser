package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/require"
)

// runAllowError executes a SQL statement and returns any error rather than
// failing the test immediately.  This lets probe tests inspect failures
// caused by unsupported syntax.
func runAllowError(t *testing.T, db *testDB, sql string) (*Result, error) {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	if err != nil {
		return nil, err
	}

	a := analyzer.New(db.cat)
	if err := a.Analyze(stmt); err != nil {
		return nil, err
	}

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	if err != nil {
		return nil, err
	}

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	if err != nil {
		return nil, err
	}

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	return Execute(pplan, ctx)
}

// TestProbe_B02_LikeEscapeClause confirms bug B02:
//   - The parser has no ESCAPE token/keyword and no AST field for it on BinaryExpr.
//   - likeMatch() (expression.go lines 851-881) has no escape-character handling.
//   - Therefore SELECT * FROM t WHERE pct LIKE '100\%' ESCAPE '\' either
//     fails to parse or returns wrong results.
//   - Additionally, without ESCAPE support there is no way to match a literal
//     '%' character: the pattern '100\%' treats '\' as a literal char and '%'
//     as a wildcard, so '100%' incorrectly matches the pattern.
func TestProbe_B02_LikeEscapeClause(t *testing.T) {
	// ── Build a tiny in-memory DB with a single TEXT column ─────────────────
	cat := catalog.New()
	store := storage.New()

	require.NoError(t, store.CreateTable("t"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name:    "t",
		Columns: []catalog.Column{{Name: "pct", Type: catalog.TypeText, Index: 0}},
	}))

	db := &testDB{cat: cat, store: store}

	// Three rows:
	//   '100%'  – literal percent sign (the only row an ESCAPE-aware engine should return)
	//   '100x'  – should never match '100\%' ESCAPE '\'
	//   '100\%' – literal backslash + percent sign
	db.run(t, `INSERT INTO t (pct) VALUES ('100%')`)
	db.run(t, `INSERT INTO t (pct) VALUES ('100x')`)
	db.run(t, `INSERT INTO t (pct) VALUES ('100\%')`)

	// ── Sub-test 1: ESCAPE clause in the SQL statement ───────────────────────
	// The SQL standard requires LIKE 'pattern' ESCAPE 'char'.
	// Because ESCAPE is not a keyword in the lexer and BinaryExpr has no
	// Escape field, the parser must either error or silently misparse.
	t.Run("EscapeClauseParseFails", func(t *testing.T) {
		result, err := runAllowError(t, db,
			`SELECT pct FROM t WHERE pct LIKE '100\%' ESCAPE '\'`)
		if err != nil {
			// Expected: ESCAPE is unrecognised and the engine reports an error.
			t.Logf("CONFIRMED (error path): ESCAPE clause caused error: %v", err)
			return
		}
		// If the engine did not error, the result must be exactly one row: '100%'.
		t.Logf("No parse/exec error — result rows: %d", len(result.Rows))
		for _, row := range result.Rows {
			t.Logf("  pct = %q", row[0].StrVal)
		}
		if len(result.Rows) != 1 || result.Rows[0][0].StrVal != "100%" {
			t.Errorf("LIKE ESCAPE returned unexpected result (want exactly [\"100%%\"]), got %d rows",
				len(result.Rows))
		}
	})

	// ── Sub-test 2: Backslash is NOT treated as an escape character ──────────
	// Without ESCAPE, the pattern '100\%' means:
	//   literal '1','0','0','\'  followed by wildcard '%' (any suffix).
	// So it should match '100\anything' — which includes '100\%' itself —
	// but NOT '100%' (which has no backslash).
	//
	// The bug: the engine will match '100%' because it simply treats '\' as
	// a literal char in the regex and '%' as .* — so any string starting with
	// "100" followed by any single character and then anything will match,
	// including "100%".  In practice '100%' (three chars after "100") does NOT
	// start with the literal '\', yet the pattern as converted to regex becomes
	// "^100\\..*$" which matches "100%" → BUG.
	t.Run("BackslashNotAnEscapeChar", func(t *testing.T) {
		result, err := runAllowError(t, db, `SELECT pct FROM t WHERE pct LIKE '100\%'`)
		if err != nil {
			t.Logf("Error: %v", err)
			return
		}
		t.Logf("LIKE '100\\%%' returned %d row(s):", len(result.Rows))
		found100Pct := false
		found100Backslash := false
		for _, row := range result.Rows {
			v := row[0].StrVal
			t.Logf("  pct = %q", v)
			if v == "100%" {
				found100Pct = true
			}
			if v == `100\%` {
				found100Backslash = true
			}
		}

		// Correct SQL-standard behaviour (no ESCAPE): '100\%' is a pattern with
		// literal backslash + wildcard.  '100%' should NOT match because it has
		// no backslash after '100'.
		if found100Pct {
			t.Errorf("BUG CONFIRMED: row '100%%' matched LIKE '100\\%%' — " +
				"engine treats '\\' as transparent, not as a literal character")
		}
		// '100\%' should match (it starts with "100\" followed by "%").
		if !found100Backslash {
			t.Logf("Note: '100\\%%' row was NOT returned (also unexpected)")
		}
	})
}
