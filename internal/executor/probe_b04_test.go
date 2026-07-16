package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_B04_LengthMultibyte probes bug B04:
// LENGTH() uses len(v.StrVal) which returns the UTF-8 byte count, not the
// Unicode character count. For multibyte characters the two values differ.
func TestProbe_B04_LengthMultibyte(t *testing.T) {
	db := newTestDB(t)

	// 'café' has 4 Unicode code points but 5 bytes in UTF-8
	// (é is encoded as 0xC3 0xA9 — two bytes).
	// SQL LENGTH is universally defined as character count → expected 4.
	result := db.run(t, "SELECT LENGTH('café')")
	require.Len(t, result.Rows, 1, "expected exactly one result row")
	require.Len(t, result.Rows[0], 1, "expected exactly one column")

	got := result.Rows[0][0].IntVal
	assert.Equal(t, int64(4), got,
		"LENGTH('café') should be 4 (character count) but got %d (likely byte count)", got)
}
