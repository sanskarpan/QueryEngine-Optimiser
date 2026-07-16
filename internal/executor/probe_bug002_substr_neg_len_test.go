package executor

// TestProbe_BUG002_SubstrNegativeLength probes BUG-002: SUBSTR with a negative
// length argument causes a Go runtime panic.
//
// At expression.go line 512, end = start + length. When length < 0, end < start.
// The subsequent slice s[start:end] triggers: runtime error: slice bounds out of range.
// There is no guard for length < 0 before the slice operation.
//
// Repro SQL:  SELECT SUBSTR('hello', 2, -1);
// Expected:   either an error or an empty string (dialect convention)
// Actual:     runtime panic

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProbe_BUG002_SubstrNegativeLength(t *testing.T) {
	db := newTestDB(t)

	// SUBSTR('hello', 2, -1): start = 2-1 = 1, length = -1, end = 1 + (-1) = 0
	// s[1:0] panics at runtime with "slice bounds out of range [1:0]"
	// We use a deferred recover to catch the panic and report it clearly.
	var panicked bool
	var panicValue interface{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicValue = r
			}
		}()
		result := db.run(t, "SELECT SUBSTR('hello', 2, -1)")
		// If we reach here, the engine handled negative length gracefully.
		assert.Len(t, result.Rows, 1, "expected 1 row")
		if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
			actual := result.Rows[0][0].StrVal
			// Per common SQL dialect convention, negative length → empty string or NULL.
			assert.True(t,
				actual == "" || result.Rows[0][0].IsNull,
				"BUG-002: SUBSTR('hello', 2, -1) should return empty string or NULL, got %q", actual)
		}
	}()

	if panicked {
		t.Errorf("BUG-002 CONFIRMED: SUBSTR('hello', 2, -1) caused a runtime panic: %v", panicValue)
	}
}
