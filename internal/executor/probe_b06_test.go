package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_B06_StringFunctionsOnNonTextValues verifies that UPPER, LENGTH, and TRIM
// operate on the string representation of non-TEXT values (INT, FLOAT, BOOL) rather
// than blindly reading the zero-value StrVal field.
func TestProbe_B06_StringFunctionsOnNonTextValues(t *testing.T) {
	db := newTestDB(t)

	// UPPER(42) should return "42", not ""
	t.Run("UPPER of integer literal", func(t *testing.T) {
		res := db.run(t, "SELECT UPPER(42)")
		require.Len(t, res.Rows, 1)
		got := res.Rows[0][0].StrVal
		assert.Equal(t, "42", got, "UPPER(42) should be '42', got %q", got)
	})

	// LENGTH(3.14) should return 4 (len("3.14")), not 0 (len(""))
	t.Run("LENGTH of float literal", func(t *testing.T) {
		res := db.run(t, "SELECT LENGTH(3.14)")
		require.Len(t, res.Rows, 1)
		got := res.Rows[0][0].IntVal
		assert.Equal(t, int64(4), got, "LENGTH(3.14) should be 4, got %d", got)
	})

	// TRIM(TRUE) should return "true", not ""
	t.Run("TRIM of boolean literal", func(t *testing.T) {
		res := db.run(t, "SELECT TRIM(TRUE)")
		require.Len(t, res.Rows, 1)
		got := res.Rows[0][0].StrVal
		assert.Equal(t, "true", got, "TRIM(TRUE) should be 'true', got %q", got)
	})
}
