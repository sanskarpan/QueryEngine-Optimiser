package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG006_MinMaxTypeInference probes BUG-006:
// MIN and MAX aggregate type inference is wrong in three places:
//
//  1. internal/planner/physical/nodes.go aggType() returns TypeText for MIN/MAX.
//  2. internal/executor/operators/hash_agg.go aggResultType() returns TypeText for MIN/MAX.
//  3. internal/executor/operators/projection.go inferColumnType() returns TypeNull
//     for any FunctionCall not explicitly listed (MIN/MAX are not listed).
//
// All three should instead return the type of the argument expression.
//
// Repro SQL (from bug report):
//
//	SELECT MAX(price) * 1.1 FROM products
//
// The schema type of MAX(price) is TypeNull (not TypeFloat as expected).
// Arithmetic still works at runtime because the actual runtime value is a float,
// but any downstream operator or client that reads the schema to determine column
// type (ORDER BY encoding, type-check layers, API serialisation) will see NULL.
func TestProbe_BUG006_MinMaxTypeInference(t *testing.T) {
	db := newTestDB(t) // loads seed data: products has price FLOAT, stock_quantity INT

	// ── Sub-test 1: schema type of MAX over a FLOAT column ──────────────────
	// Expected: TypeFloat.  Actual with bug: TypeNull.
	t.Run("MAX_price_schema_type_is_float", func(t *testing.T) {
		result := db.run(t, "SELECT MAX(price) FROM products")
		require.Len(t, result.Columns, 1, "expected one output column")
		require.Len(t, result.Schema, 1, "expected one schema entry")

		got := result.Schema[0].Type
		if got != catalog.TypeFloat {
			t.Errorf(
				"BUG-006 CONFIRMED: MAX(price) schema type = %s, want %s; "+
					"inferColumnType in projection.go returns TypeNull for MIN/MAX FunctionCalls",
				got, catalog.TypeFloat,
			)
		} else {
			t.Logf("MAX(price) schema type = %s (correct)", got)
		}
	})

	// ── Sub-test 2: schema type of MIN over a FLOAT column ──────────────────
	t.Run("MIN_price_schema_type_is_float", func(t *testing.T) {
		result := db.run(t, "SELECT MIN(price) FROM products")
		require.Len(t, result.Schema, 1)

		got := result.Schema[0].Type
		if got != catalog.TypeFloat {
			t.Errorf(
				"BUG-006 CONFIRMED: MIN(price) schema type = %s, want %s",
				got, catalog.TypeFloat,
			)
		} else {
			t.Logf("MIN(price) schema type = %s (correct)", got)
		}
	})

	// ── Sub-test 3: schema type of MAX over an INT column ───────────────────
	t.Run("MAX_stock_quantity_schema_type_is_int", func(t *testing.T) {
		result := db.run(t, "SELECT MAX(stock_quantity) FROM products")
		require.Len(t, result.Schema, 1)

		got := result.Schema[0].Type
		if got != catalog.TypeInt {
			t.Errorf(
				"BUG-006 CONFIRMED: MAX(stock_quantity) schema type = %s, want %s",
				got, catalog.TypeInt,
			)
		} else {
			t.Logf("MAX(stock_quantity) schema type = %s (correct)", got)
		}
	})

	// ── Sub-test 4: repro SQL — MAX(price) * 1.1 must produce a numeric result ──
	// The schema type of the expression itself happens to be TypeFloat because
	// inferColumnType for BinaryExpr checks `lt == TypeFloat || rt == TypeFloat`,
	// and the right operand 1.1 (FloatLiteral) forces TypeFloat.
	// Runtime arithmetic also works because the actual stored MAX value is a float.
	// This sub-test verifies the runtime result is correct despite the schema bug.
	t.Run("MAX_price_multiply_float_numeric_result", func(t *testing.T) {
		result := db.run(t, "SELECT MAX(price) * 1.1 FROM products")
		require.Len(t, result.Rows, 1, "expected exactly one output row")
		require.Len(t, result.Rows[0], 1, "expected exactly one column value")

		val := result.Rows[0][0]
		if val.IsNull {
			t.Fatal("MAX(price) * 1.1 returned NULL; expected a numeric float")
		}
		if val.Type != catalog.TypeFloat && val.Type != catalog.TypeInt {
			t.Errorf(
				"BUG-006: MAX(price) * 1.1 returned value of type %s (value=%v), want TypeFloat; "+
					"multiplication with float should produce a numeric result",
				val.Type, val,
			)
		} else {
			t.Logf("MAX(price) * 1.1 = %v (type %s) — runtime arithmetic OK despite schema bug", val, val.Type)
		}
	})

	// ── Sub-test 5: MIN(stock_quantity) * 2 — INT column variant ────────────
	t.Run("MIN_stock_quantity_schema_type_is_int", func(t *testing.T) {
		result := db.run(t, "SELECT MIN(stock_quantity) FROM products")
		require.Len(t, result.Schema, 1)

		got := result.Schema[0].Type
		if got != catalog.TypeInt {
			t.Errorf(
				"BUG-006 CONFIRMED: MIN(stock_quantity) schema type = %s, want %s",
				got, catalog.TypeInt,
			)
		} else {
			t.Logf("MIN(stock_quantity) schema type = %s (correct)", got)
		}
	})
}
