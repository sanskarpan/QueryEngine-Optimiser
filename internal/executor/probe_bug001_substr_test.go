package executor

// TestProbe_BUG001_SubstrMultiByte probes BUG-001: SUBSTR uses raw byte
// slicing (s[start:end]) on Go strings rather than rune-aware indexing.
// For multi-byte UTF-8 characters (é = 2 bytes, 中 = 3 bytes, emoji = 4 bytes),
// the computed byte offset will split a codepoint mid-sequence, returning
// garbled text or causing a runtime panic.
//
// Repro SQL:  SELECT SUBSTR('héllo', 2, 3);
// Expected:   'éll'  (rune-based: runes 2-4 = é, l, l)
// Actual:     corrupted bytes or wrong slice due to é being 2 bytes (0xC3 0xA9),
//             so byte index 1 lands inside the 2-byte é sequence.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProbe_BUG001_SubstrMultiByte(t *testing.T) {
	db := newTestDB(t)

	// 'héllo': h=1 byte, é=2 bytes (0xC3 0xA9), l=1, l=1, o=1 → 6 bytes total
	// SQL SUBSTR is 1-based character indexing.
	// SUBSTR('héllo', 2, 3) should return characters at positions 2,3,4 = 'é','l','l' = "éll"
	// With raw byte indexing: start = 2-1 = 1, end = 1+3 = 4
	// s[1:4] on "héllo" (bytes: h=0x68, é=0xC3,0xA9, l=0x6C, l=0x6C, o=0x6F)
	// s[1:4] = bytes 0xC3,0xA9,0x6C = "él" (partially correct by accident for é)
	// Actually: byte 0 = 'h', byte 1 = 0xC3 (first byte of é), byte 2 = 0xA9 (second byte of é),
	//           byte 3 = 'l', byte 4 = 'l'
	// s[1:4] = 0xC3, 0xA9, 0x6C = "él" — only 2 characters instead of 3,
	// and byte 1 starts mid-character-boundary (it starts the é sequence, which is OK here),
	// but the length count is wrong: 3 bytes ≠ 3 characters.
	result := db.run(t, "SELECT SUBSTR('héllo', 2, 3)")
	assert.Len(t, result.Rows, 1, "expected 1 row from SUBSTR query")
	if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		actual := result.Rows[0][0].StrVal
		assert.Equal(t, "éll", actual,
			"BUG-001: SUBSTR('héllo', 2, 3) should return 'éll' (rune-aware), got %q (byte-indexed). "+
				"'é' is 2 bytes so byte slicing shifts the character count.", actual)
	}

	// Additional case: Chinese characters (3 bytes each)
	// SUBSTR('中文abc', 1, 2) should return '中文'
	result2 := db.run(t, "SELECT SUBSTR('中文abc', 1, 2)")
	assert.Len(t, result2.Rows, 1, "expected 1 row from SUBSTR query")
	if len(result2.Rows) > 0 && len(result2.Rows[0]) > 0 {
		actual2 := result2.Rows[0][0].StrVal
		assert.Equal(t, "中文", actual2,
			"BUG-001: SUBSTR('中文abc', 1, 2) should return '中文' (rune-aware), got %q (byte-indexed). "+
				"Each Chinese char is 3 bytes, so byte slicing returns corrupted output.", actual2)
	}
}
