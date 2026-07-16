package executor

import (
	"fmt"
	"strings"
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

// newTestDBWithTable returns a fresh db that has a table named "t" with
// columns (name TEXT, age INT) pre-registered in both catalog and storage.
// It also inserts the two seed rows from the bug report.
func newTestDBWithTable(t *testing.T) *testDB {
	t.Helper()
	cat := catalog.New()
	store := storage.New()

	// Register the table in the catalog directly (CREATE TABLE via the
	// logical planner is unsupported; the API handler does it via a separate code path).
	tbl := &catalog.Table{
		Name: "t",
		Columns: []catalog.Column{
			{Name: "name", Type: catalog.TypeText, Index: 0},
			{Name: "age", Type: catalog.TypeInt, Index: 1},
		},
	}
	require.NoError(t, cat.Register(tbl))
	require.NoError(t, store.CreateTable("t"))

	db := &testDB{cat: cat, store: store}

	// Insert rows using the normal INSERT path (logical planner supports INSERT).
	db.run(t, "INSERT INTO t (name, age) VALUES ('Alice', 30)")
	db.run(t, "INSERT INTO t (name, age) VALUES ('Alice', 25)")

	return db
}

// analyzeQuery parses and analyzes a single SQL statement against db's catalog.
// Returns the analyzer error (nil = analysis passed).
func analyzeQuery(t *testing.T, db *testDB, sql string) error {
	t.Helper()
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse failed for: %s", sql)
	a := analyzer.New(db.cat)
	return a.Analyze(stmt)
}

// executeRaw runs the full pipeline for sql against db WITHOUT calling
// require.NoError on the analysis step — it lets the bug be observable at
// execution time when the analysis step incorrectly passes.
func executeRaw(t *testing.T, db *testDB, sql string) (*Result, error) {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse: %s", sql)

	a := analyzer.New(db.cat)
	_ = a.Analyze(stmt) // intentionally ignoring analysis error to reach execution

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	if err != nil {
		return nil, fmt.Errorf("logical build: %w", err)
	}

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	if err != nil {
		return nil, fmt.Errorf("physical build: %w", err)
	}

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	return Execute(pplan, ctx)
}

// TestProbe_BUG006_NonAggColumnNotInGroupBy verifies BUG-006:
//
// The analyzer's mix-check (analyzer.go lines 232-237) only fires when there
// is NO GROUP BY. When a GROUP BY is present, non-aggregate SELECT columns
// that do not appear in the GROUP BY expression list are silently accepted.
//
// Repro:
//
//	CREATE TABLE t (name TEXT, age INT);
//	INSERT INTO t VALUES ('Alice',30),('Alice',25);
//	SELECT name, age, COUNT(*) FROM t GROUP BY name;
//
// Expected: analysis error — column 'age' must appear in GROUP BY or aggregate.
// Actual (bug):   no error; query executes and 'age' evaluates to NULL silently.
func TestProbe_BUG006_NonAggColumnNotInGroupBy(t *testing.T) {
	db := newTestDBWithTable(t)

	// The column 'age' is NOT in the GROUP BY list. A conforming analyzer must
	// reject this with an error.
	badQuery := "SELECT name, age, COUNT(*) FROM t GROUP BY name"

	analyzeErr := analyzeQuery(t, db, badQuery)

	if analyzeErr != nil {
		// The analyzer correctly rejected the query — bug is NOT present.
		t.Logf("Analyzer rejected query with: %v", analyzeErr)
		msg := strings.ToLower(analyzeErr.Error())
		_ = msg
		// Test passes — correct behavior.
		return
	}

	// analyzeErr is nil: the analyzer accepted the query without error.
	// This confirms BUG-006 is present. Now run to execution to capture the
	// silent-NULL symptom described in the bug report.
	t.Logf("BUG-006 CONFIRMED: analyzer returned nil (no error) for query: %s", badQuery)

	result, execErr := executeRaw(t, db, badQuery)

	var actualOutput string
	if execErr != nil {
		actualOutput = fmt.Sprintf("execution error: %v", execErr)
		t.Logf("Execution error: %v", execErr)
	} else if result != nil {
		t.Logf("Query executed successfully with %d row(s), columns: %v",
			len(result.Rows), result.Columns)
		for i, row := range result.Rows {
			vals := make([]string, len(row))
			for j, v := range row {
				vals[j] = v.String()
			}
			line := fmt.Sprintf("row[%d]: %s", i, strings.Join(vals, ", "))
			t.Logf("  %s", line)
			actualOutput += line + "\n"
		}
	}

	// Fail the test to surface the bug with a descriptive message.
	t.Fatalf(
		"BUG-006: analyzer accepted 'SELECT name, age, COUNT(*) FROM t GROUP BY name' without error. "+
			"Column 'age' is not in GROUP BY and is not an aggregate — it must be rejected. "+
			"Actual execution output:\n%s",
		actualOutput,
	)
}
