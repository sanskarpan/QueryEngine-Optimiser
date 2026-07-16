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
	"github.com/stretchr/testify/require"
)

// runStmt is a lower-level helper that runs a single already-parsed statement
// using the provided db, returning a *Result. It does NOT call parser.New —
// the caller already has an in-progress parser.
func runStmt(t *testing.T, db *testDB, p *parser.Parser) *Result {
	t.Helper()

	stmt, err := p.ParseStatement()
	require.NoError(t, err, "ParseStatement failed")

	a := analyzer.New(db.cat)
	require.NoError(t, a.Analyze(stmt), "analyze failed")

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	require.NoError(t, err, "logical build failed")

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	require.NoError(t, err, "physical build failed")

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	result, err := Execute(pplan, ctx)
	require.NoError(t, err, "execute failed")
	return result
}

// TestProbe_Bug008_MultiStatementSemicolon verifies BUG-008:
// ParseStatement does not consume the trailing semicolon, so calling it
// twice on the input "SELECT 1; SELECT 2" leaves current=SEMI on the
// second call, which hits the default case and returns an error
// 'expected SELECT … got ;'.
func TestProbe_Bug008_MultiStatementSemicolon(t *testing.T) {
	db := newTestDB(t)

	// Use a single parser instance for both statements, mirroring what a
	// multi-statement caller would do.
	p := parser.New("SELECT 1; SELECT 2")

	// First statement: SELECT 1 — should succeed.
	r1 := runStmt(t, db, p)
	require.Len(t, r1.Rows, 1, "first statement (SELECT 1) should return 1 row")
	require.Equal(t, int64(1), r1.Rows[0][0].IntVal,
		"first statement result value should be 1")

	// The parser must consume the semicolon before the second call.
	// BUG-008: if ParseStatement leaves current=SEMI, the next call fails with
	// "expected SELECT … got ;"
	// Second statement: SELECT 2 — must also succeed.
	r2 := runStmt(t, db, p)
	require.Len(t, r2.Rows, 1, "second statement (SELECT 2) should return 1 row")
	require.Equal(t, int64(2), r2.Rows[0][0].IntVal,
		"second statement result value should be 2")

	_ = catalog.TypeInt // import used via result values
}
