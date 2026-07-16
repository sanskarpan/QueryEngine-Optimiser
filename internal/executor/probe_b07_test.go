package executor

// TestProbe_B07_ToFloatCoercion verifies bug B07:
// toFloat() in expression.go only handles TypeInt explicitly;
// TypeBool and TypeText fall through to return v.FloatVal (zero).
// So ROUND(TRUE) should return 1 (bool true -> 1.0) and
// FLOOR('3.7') should return 3 (text "3.7" -> 3.7 -> floor = 3).

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
)

func TestProbe_B07_ToFloatCoercion(t *testing.T) {
	db := newTestDB(t)

	// ROUND(TRUE): bool true should coerce to 1.0, ROUND(1.0) = 1.0
	// Bug: toFloat(BoolValue(true)) returns 0.0 instead of 1.0 → ROUND returns 0.0
	t.Run("ROUND_bool_true", func(t *testing.T) {
		res := db.run(t, "SELECT ROUND(TRUE)")
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			t.Fatal("expected at least one row/column")
		}
		got := res.Rows[0][0]
		t.Logf("ROUND(TRUE) => type=%v value=%v floatVal=%v", got.Type, got, got.FloatVal)
		// Expected: 1 (float 1.0), as TRUE coerces to numeric 1
		if got.Type == catalog.TypeFloat && got.FloatVal == 1.0 {
			t.Logf("PASS: ROUND(TRUE) = 1.0 (correct)")
		} else {
			t.Errorf("FAIL: ROUND(TRUE) returned %v (type=%v), expected 1.0", got, got.Type)
		}
	})

	// FLOOR('3.7'): text "3.7" should parse to 3.7, FLOOR(3.7) = 3.0
	// Bug: toFloat(TextValue("3.7")) returns 0.0 instead of 3.7 → FLOOR returns 0.0
	t.Run("FLOOR_text_3_7", func(t *testing.T) {
		res := db.run(t, "SELECT FLOOR('3.7')")
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			t.Fatal("expected at least one row/column")
		}
		got := res.Rows[0][0]
		t.Logf("FLOOR('3.7') => type=%v value=%v floatVal=%v", got.Type, got, got.FloatVal)
		// Expected: 3.0, as "3.7" coerces to numeric 3.7, floor = 3.0
		if got.Type == catalog.TypeFloat && got.FloatVal == 3.0 {
			t.Logf("PASS: FLOOR('3.7') = 3.0 (correct)")
		} else {
			t.Errorf("FAIL: FLOOR('3.7') returned %v (type=%v), expected 3.0", got, got.Type)
		}
	})
}
