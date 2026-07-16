package catalog

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// DataType identifies the SQL data type of a value or column.
type DataType int

const (
	TypeNull  DataType = iota
	TypeInt            // INT / INTEGER
	TypeFloat          // FLOAT
	TypeText           // TEXT / VARCHAR
	TypeBool           // BOOL / BOOLEAN
)

func (d DataType) String() string {
	switch d {
	case TypeNull:
		return "NULL"
	case TypeInt:
		return "INT"
	case TypeFloat:
		return "FLOAT"
	case TypeText:
		return "TEXT"
	case TypeBool:
		return "BOOL"
	default:
		return "UNKNOWN"
	}
}

// ParseDataType converts a type string to DataType.
func ParseDataType(s string) (DataType, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "INT", "INTEGER":
		return TypeInt, nil
	case "FLOAT", "REAL", "DOUBLE":
		return TypeFloat, nil
	case "TEXT", "VARCHAR", "STRING":
		return TypeText, nil
	case "BOOL", "BOOLEAN":
		return TypeBool, nil
	case "NULL":
		return TypeNull, nil
	default:
		// Handle VARCHAR(n)
		if strings.HasPrefix(strings.ToUpper(s), "VARCHAR") {
			return TypeText, nil
		}
		return TypeNull, fmt.Errorf("unknown data type: %q", s)
	}
}

// ErrNullComparison is returned when comparing NULL values.
var ErrNullComparison = errors.New("cannot compare NULL values")

// ErrTypeMismatch is returned on incompatible type operations.
var ErrTypeMismatch = errors.New("type mismatch")

// Value represents a single SQL value.
type Value struct {
	Type     DataType
	IsNull   bool
	IntVal   int64
	FloatVal float64
	StrVal   string
	BoolVal  bool
}

// NullValue returns a NULL value.
func NullValue() Value { return Value{Type: TypeNull, IsNull: true} }

// IntValue returns an integer value.
func IntValue(v int64) Value { return Value{Type: TypeInt, IntVal: v} }

// FloatValue returns a float value.
func FloatValue(v float64) Value { return Value{Type: TypeFloat, FloatVal: v} }

// TextValue returns a text value.
func TextValue(v string) Value { return Value{Type: TypeText, StrVal: v} }

// BoolValue returns a boolean value.
func BoolValue(v bool) Value { return Value{Type: TypeBool, BoolVal: v} }

// String returns a display string for the value.
func (v Value) String() string {
	if v.IsNull {
		return "NULL"
	}
	switch v.Type {
	case TypeInt:
		return fmt.Sprintf("%d", v.IntVal)
	case TypeFloat:
		return fmt.Sprintf("%g", v.FloatVal)
	case TypeText:
		return v.StrVal
	case TypeBool:
		if v.BoolVal {
			return "true"
		}
		return "false"
	default:
		return "NULL"
	}
}

// toFloat converts a numeric value to float64 for mixed arithmetic.
func (v Value) toFloat() float64 {
	if v.Type == TypeInt {
		return float64(v.IntVal)
	}
	return v.FloatVal
}

// Compare compares two values. Returns -1, 0, or 1.
// Returns ErrNullComparison if either value is NULL.
// Returns ErrTypeMismatch if types are incompatible.
func (v Value) Compare(other Value) (int, error) {
	if v.IsNull || other.IsNull {
		return 0, ErrNullComparison
	}

	// Numeric coercion: INT op FLOAT → both float
	if (v.Type == TypeInt || v.Type == TypeFloat) && (other.Type == TypeInt || other.Type == TypeFloat) {
		a, b := v.toFloat(), other.toFloat()
		if a < b {
			return -1, nil
		} else if a > b {
			return 1, nil
		}
		return 0, nil
	}

	if v.Type != other.Type {
		return 0, fmt.Errorf("%w: cannot compare %s and %s", ErrTypeMismatch, v.Type, other.Type)
	}

	switch v.Type {
	case TypeText:
		if v.StrVal < other.StrVal {
			return -1, nil
		} else if v.StrVal > other.StrVal {
			return 1, nil
		}
		return 0, nil
	case TypeBool:
		if v.BoolVal == other.BoolVal {
			return 0, nil
		}
		if !v.BoolVal {
			return -1, nil
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("%w: unsupported type for comparison: %s", ErrTypeMismatch, v.Type)
	}
}

// Add returns v + other.
func (v Value) Add(other Value) (Value, error) {
	if v.IsNull || other.IsNull {
		return NullValue(), nil
	}
	if v.Type == TypeText && other.Type == TypeText {
		return TextValue(v.StrVal + other.StrVal), nil
	}
	if (v.Type == TypeInt || v.Type == TypeFloat) && (other.Type == TypeInt || other.Type == TypeFloat) {
		if v.Type == TypeFloat || other.Type == TypeFloat {
			return FloatValue(v.toFloat() + other.toFloat()), nil
		}
		// Overflow guard: if signs are the same and result overflows, return error.
		a, b := v.IntVal, other.IntVal
		if b > 0 && a > math.MaxInt64-b {
			return NullValue(), fmt.Errorf("integer overflow in addition")
		}
		if b < 0 && a < math.MinInt64-b {
			return NullValue(), fmt.Errorf("integer overflow in addition")
		}
		return IntValue(a + b), nil
	}
	return NullValue(), fmt.Errorf("%w: cannot add %s and %s", ErrTypeMismatch, v.Type, other.Type)
}

// Sub returns v - other.
func (v Value) Sub(other Value) (Value, error) {
	if v.IsNull || other.IsNull {
		return NullValue(), nil
	}
	if (v.Type == TypeInt || v.Type == TypeFloat) && (other.Type == TypeInt || other.Type == TypeFloat) {
		if v.Type == TypeFloat || other.Type == TypeFloat {
			return FloatValue(v.toFloat() - other.toFloat()), nil
		}
		a, b := v.IntVal, other.IntVal
		if b < 0 && a > math.MaxInt64+b {
			return NullValue(), fmt.Errorf("integer overflow in subtraction")
		}
		if b > 0 && a < math.MinInt64+b {
			return NullValue(), fmt.Errorf("integer overflow in subtraction")
		}
		return IntValue(a - b), nil
	}
	return NullValue(), fmt.Errorf("%w: cannot subtract %s and %s", ErrTypeMismatch, v.Type, other.Type)
}

// Mul returns v * other.
func (v Value) Mul(other Value) (Value, error) {
	if v.IsNull || other.IsNull {
		return NullValue(), nil
	}
	if (v.Type == TypeInt || v.Type == TypeFloat) && (other.Type == TypeInt || other.Type == TypeFloat) {
		if v.Type == TypeFloat || other.Type == TypeFloat {
			return FloatValue(v.toFloat() * other.toFloat()), nil
		}
		a, b := v.IntVal, other.IntVal
		if a != 0 && b != 0 {
			if (b > 0 && a > math.MaxInt64/b) || (b > 0 && a < math.MinInt64/b) ||
				(b < 0 && a < math.MaxInt64/b) || (b == -1 && a == math.MinInt64) ||
				(b < 0 && b != -1 && a > math.MinInt64/b) {
				return NullValue(), fmt.Errorf("integer overflow in multiplication")
			}
		}
		return IntValue(a * b), nil
	}
	return NullValue(), fmt.Errorf("%w: cannot multiply %s and %s", ErrTypeMismatch, v.Type, other.Type)
}

// Div returns v / other.
func (v Value) Div(other Value) (Value, error) {
	if v.IsNull || other.IsNull {
		return NullValue(), nil
	}
	if (v.Type == TypeInt || v.Type == TypeFloat) && (other.Type == TypeInt || other.Type == TypeFloat) {
		if other.toFloat() == 0 {
			return NullValue(), fmt.Errorf("division by zero")
		}
		if v.Type == TypeFloat || other.Type == TypeFloat {
			return FloatValue(v.toFloat() / other.toFloat()), nil
		}
		return IntValue(v.IntVal / other.IntVal), nil
	}
	return NullValue(), fmt.Errorf("%w: cannot divide %s and %s", ErrTypeMismatch, v.Type, other.Type)
}

// Mod returns v % other.
func (v Value) Mod(other Value) (Value, error) {
	if v.IsNull || other.IsNull {
		return NullValue(), nil
	}
	if v.Type == TypeInt && other.Type == TypeInt {
		if other.IntVal == 0 {
			return NullValue(), fmt.Errorf("division by zero")
		}
		return IntValue(v.IntVal % other.IntVal), nil
	}
	return NullValue(), fmt.Errorf("%w: MOD only supported for integers", ErrTypeMismatch)
}
