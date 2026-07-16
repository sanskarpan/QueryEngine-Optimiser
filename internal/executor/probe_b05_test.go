package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProbe_B05_SubstringUnicode checks that SUBSTRING correctly handles
// multibyte UTF-8 characters (rune-based indexing, not byte-based).
func TestProbe_B05_SubstringUnicode(t *testing.T) {
	db := newTestDB(t)

	// 'héllo': h=1byte, é=2bytes, l=1byte, l=1byte, o=1byte
	// SQL 1-based: position 2 is 'é', length 3 => 'éll'
	result := db.run(t, "SELECT SUBSTRING('héllo', 2, 3)")
	if assert.Equal(t, 1, len(result.Rows), "expected exactly one row") {
		if assert.Equal(t, 1, len(result.Rows[0]), "expected exactly one column") {
			actual := result.Rows[0][0].StrVal
			t.Logf("SUBSTRING('héllo', 2, 3) returned: %q (bytes: %v)", actual, []byte(actual))
			assert.Equal(t, "éll", actual,
				"SUBSTRING should return rune-based slice, not byte-based")
		}
	}
}
