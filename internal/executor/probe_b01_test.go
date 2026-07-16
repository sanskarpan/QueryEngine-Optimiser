package executor

// Probe test for bug B01: SUBSTRING panics when the length argument is negative.
// In expression.go lines 511-516, end = start + length can produce end < start,
// causing a Go runtime panic on the s[start:end] slice expression.

import (
	"testing"
)

func TestProbe_B01_SubstringNegativeLength(t *testing.T) {
	db := newTestDB(t)
	// SELECT SUBSTRING('hello', 2, -1) — start=1 (0-based after -1 adjustment),
	// length=-1, so end = 1 + (-1) = 0 which is < start=1 → panic.
	result := db.run(t, "SELECT SUBSTRING('hello', 2, -1)")
	// If we reach here, no panic occurred. The result should be either an empty
	// string or NULL (both are acceptable for negative-length semantics).
	if len(result.Rows) == 0 {
		t.Fatal("expected at least one row in result")
	}
	val := result.Rows[0][0]
	if !val.IsNull && val.StrVal != "" {
		t.Errorf("expected empty string or NULL for negative length, got %q", val.StrVal)
	}
}
