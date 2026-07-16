package executor

// TestProbe_CTE05_SelfReferentialCTEStackOverflow probes CTE-05:
// A self-referential CTE without the RECURSIVE keyword causes infinite plan-time
// recursion in buildTableRef (internal/planner/logical/builder.go:318-329).
//
// When buildTableRef encounters a table reference whose name matches an entry in
// b.ctes, it calls b.buildSelect(cteSel). If that CTE body itself references the
// same CTE name, buildSelect calls buildTableRef again for the same name, which
// calls buildSelect again, and so on — indefinitely.
//
// There is no recursion depth guard at plan time. The maxSubqueryDepth=8 guard in
// expression.go:843 only protects runtime subquery evaluation, not plan-time
// CTE expansion.
//
// Repro SQL: WITH t AS (SELECT id FROM t WHERE id < 5) SELECT * FROM t
//
// Expected: a planning error (e.g. "recursive CTE requires RECURSIVE keyword"
//           or similar), not a goroutine stack overflow / program crash.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProbe_CTE05_SelfReferentialCTEStackOverflow(t *testing.T) {
	db := newTestDB(t)

	const reproSQL = `WITH t AS (SELECT id FROM t WHERE id < 5) SELECT * FROM t`

	// Run the query in a goroutine with a timeout so that an infinite recursion
	// (stack overflow) is detected rather than hanging the test suite.
	type outcome struct {
		result *Result
		err    error
	}
	ch := make(chan outcome, 1)

	go func() {
		result, err := runAllowError(t, db, reproSQL)
		ch <- outcome{result, err}
	}()

	select {
	case out := <-ch:
		if out.err != nil {
			// Any error is acceptable — the engine correctly rejected the query
			// rather than overflowing the goroutine stack.
			t.Logf("CTE-05: engine returned an error (expected): %v", out.err)
			t.Logf("CTE-05 NOT confirmed as stack-overflow: query was rejected with an error")
			// This is the desired behaviour; the test passes by not panicking.
		} else {
			// Returned a result with no error — this is unexpected. Log details.
			t.Logf("CTE-05: engine returned %d row(s) with no error (unexpected)", len(out.result.Rows))
			// This should not happen either, but it is not a stack overflow crash.
			assert.Fail(t, "CTE-05: self-referential CTE (no RECURSIVE) should produce an error, got rows instead")
		}

	case <-time.After(5 * time.Second):
		// The goroutine did not complete within 5 seconds — this is the
		// symptom of infinite recursion / stack overflow. The goroutine likely
		// crashed the process or is spinning. We cannot recover it, so we mark
		// the bug as confirmed and fail.
		t.Errorf("CTE-05 CONFIRMED: self-referential CTE caused the planner to hang or crash " +
			"(goroutine did not complete within 5 s — likely goroutine stack overflow). " +
			"Root cause: buildTableRef in builder.go:318-329 calls buildSelect(cteSel) " +
			"without a recursion depth guard; when the CTE body references its own name " +
			"the planner recurses infinitely.")
	}
}
