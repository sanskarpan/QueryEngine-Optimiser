package operators

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/lexer"
)

// likeCache caches compiled LIKE patterns to avoid re-compilation on every row.
// Bounded to maxLikeCacheSize entries; older entries are not evicted but new ones
// are simply not cached once the cap is reached.
var (
	likeCache        sync.Map
	likeCacheSize    int64
	maxLikeCacheSize int64 = 1024
)

const maxSubqueryDepth = 8

// EvalExpr evaluates an expression against a tuple.
// ctx is used for subquery evaluation (EXISTS, IN subquery, scalar subquery);
// it may be nil when evaluating constant expressions (e.g. LIMIT values).
func EvalExpr(expr ast.Expression, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	if expr == nil {
		return catalog.NullValue(), nil
	}
	switch e := expr.(type) {
	case *ast.IntLiteral:
		return catalog.IntValue(e.Value), nil

	case *ast.FloatLiteral:
		return catalog.FloatValue(e.Value), nil

	case *ast.StringLiteral:
		return catalog.TextValue(e.Value), nil

	case *ast.BoolLiteral:
		return catalog.BoolValue(e.Value), nil

	case *ast.NullLiteral:
		return catalog.NullValue(), nil

	case *ast.ColumnRef:
		return resolveColumn(e, tuple, ctx)

	case *ast.BinaryExpr:
		return evalBinary(e, tuple, ctx)

	case *ast.UnaryExpr:
		return evalUnary(e, tuple, ctx)

	case *ast.FunctionCall:
		return evalFunction(e, tuple, ctx)

	case *ast.CaseExpr:
		return evalCase(e, tuple, ctx)

	case *ast.IsNullExpr:
		val, err := EvalExpr(e.Expr, tuple, ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
		result := val.IsNull
		if e.Negated {
			result = !result
		}
		return catalog.BoolValue(result), nil

	case *ast.InExpr:
		return evalIn(e, tuple, ctx)

	case *ast.BetweenExpr:
		return evalBetween(e, tuple, ctx)

	case *ast.AliasExpr:
		return EvalExpr(e.Expr, tuple, ctx)

	case *ast.StarExpr:
		return catalog.IntValue(1), nil // COUNT(*) placeholder

	case *ast.ExistsExpr:
		return evalExists(e, tuple, ctx)

	case *ast.SubqueryExpr:
		return evalSubqueryScalar(e, tuple, ctx)

	case *ast.CastExpr:
		return evalCast(e, tuple, ctx)

	case *ast.ExtractExpr:
		return evalExtract(e, tuple, ctx)

	case *ast.WindowFuncExpr:
		// Window functions are pre-computed by WindowOp; here they appear as ColumnRef already.
		// If we reach this, just evaluate the inner function (fallback for ungrouped contexts).
		return evalFunction(e.Func, tuple, ctx)

	default:
		return catalog.NullValue(), fmt.Errorf("unsupported expression type: %T", expr)
	}
}

// evalExtract evaluates EXTRACT(part FROM date_expr).
func evalExtract(e *ast.ExtractExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	v, err := EvalExpr(e.From, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	if v.IsNull {
		return catalog.NullValue(), nil
	}
	return extractDatePart(e.Part, v)
}

func evalCast(e *ast.CastExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	v, err := EvalExpr(e.Expr, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	if v.IsNull {
		return catalog.NullValue(), nil
	}
	switch strings.ToUpper(e.TypeName) {
	case "INT", "INTEGER":
		switch v.Type {
		case catalog.TypeInt:
			return v, nil
		case catalog.TypeFloat:
			return catalog.IntValue(int64(v.FloatVal)), nil
		case catalog.TypeText:
			n, err := strconv.ParseInt(strings.TrimSpace(v.StrVal), 10, 64)
			if err != nil {
				return catalog.NullValue(), fmt.Errorf("CAST: cannot convert %q to INT", v.StrVal)
			}
			return catalog.IntValue(n), nil
		case catalog.TypeBool:
			if v.BoolVal {
				return catalog.IntValue(1), nil
			}
			return catalog.IntValue(0), nil
		}
	case "FLOAT", "REAL", "DOUBLE":
		switch v.Type {
		case catalog.TypeFloat:
			return v, nil
		case catalog.TypeInt:
			return catalog.FloatValue(float64(v.IntVal)), nil
		case catalog.TypeText:
			f, err := strconv.ParseFloat(strings.TrimSpace(v.StrVal), 64)
			if err != nil {
				return catalog.NullValue(), fmt.Errorf("CAST: cannot convert %q to FLOAT", v.StrVal)
			}
			return catalog.FloatValue(f), nil
		case catalog.TypeBool:
			if v.BoolVal {
				return catalog.FloatValue(1), nil
			}
			return catalog.FloatValue(0), nil
		}
	case "TEXT", "VARCHAR", "STRING":
		return catalog.TextValue(v.String()), nil
	case "BOOL", "BOOLEAN":
		switch v.Type {
		case catalog.TypeBool:
			return v, nil
		case catalog.TypeInt:
			return catalog.BoolValue(v.IntVal != 0), nil
		case catalog.TypeFloat:
			return catalog.BoolValue(v.FloatVal != 0), nil
		case catalog.TypeText:
			switch strings.ToLower(strings.TrimSpace(v.StrVal)) {
			case "true", "1", "yes":
				return catalog.BoolValue(true), nil
			case "false", "0", "no":
				return catalog.BoolValue(false), nil
			}
			return catalog.NullValue(), fmt.Errorf("CAST: cannot convert %q to BOOL", v.StrVal)
		}
	}
	return catalog.NullValue(), fmt.Errorf("CAST: unsupported cast to %s", e.TypeName)
}

func resolveColumn(ref *ast.ColumnRef, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	if tuple == nil {
		return catalog.NullValue(), fmt.Errorf("no tuple context for column %s", ref.Column)
	}

	if v, ok := lookupColumnInTuple(ref, tuple); ok {
		return v, nil
	}

	// Correlated subquery: try the outer tuple when column not found in inner tuple.
	if ctx != nil && ctx.OuterTuple != nil {
		if v, ok := lookupColumnInTuple(ref, ctx.OuterTuple); ok {
			return v, nil
		}
	}

	return catalog.NullValue(), fmt.Errorf("column %q not found in tuple schema", ref.Column)
}

func lookupColumnInTuple(ref *ast.ColumnRef, tuple *exectypes.Tuple) (catalog.Value, bool) {
	// Step 1: exact ResolvedTable.Column match (most specific)
	if ref.ResolvedTable != "" {
		target := ref.ResolvedTable + "." + ref.Column
		for i, col := range tuple.Schema {
			if col.Name == target {
				if i < len(tuple.Values) {
					return tuple.Values[i], true
				}
			}
		}
	}

	colLower := strings.ToLower(ref.Column)
	tableLower := strings.ToLower(ref.Table)

	// Step 2: suffix match with table prefix filter (e.g. "customers.name" matches alias "customers")
	for i, col := range tuple.Schema {
		name := strings.ToLower(col.Name)
		if strings.HasSuffix(name, "."+colLower) {
			if tableLower == "" || strings.HasPrefix(name, tableLower+".") {
				if i < len(tuple.Values) {
					return tuple.Values[i], true
				}
			}
		}
		if name == colLower {
			if i < len(tuple.Values) {
				return tuple.Values[i], true
			}
		}
	}

	// Step 3: suffix-only fallback — handles CTE alias vs underlying table name mismatches
	// e.g. SELECT c1.name where schema has "customers.name" (CTE alias ≠ real table name)
	for i, col := range tuple.Schema {
		name := strings.ToLower(col.Name)
		if strings.HasSuffix(name, "."+colLower) {
			if i < len(tuple.Values) {
				return tuple.Values[i], true
			}
		}
	}

	return catalog.NullValue(), false
}

func evalBinary(e *ast.BinaryExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	left, err := EvalExpr(e.Left, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	right, err := EvalExpr(e.Right, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}

	switch e.Op.Type {
	case lexer.PLUS:
		return left.Add(right)
	case lexer.MINUS:
		return left.Sub(right)
	case lexer.STAR:
		return left.Mul(right)
	case lexer.SLASH:
		return left.Div(right)
	case lexer.PERCENT:
		return left.Mod(right)
	case lexer.CONCAT:
		if left.IsNull || right.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(left.String() + right.String()), nil

	case lexer.AND:
		// Short-circuit: false AND anything = false
		if !left.IsNull && left.Type == catalog.TypeBool && !left.BoolVal {
			return catalog.BoolValue(false), nil
		}
		if !right.IsNull && right.Type == catalog.TypeBool && !right.BoolVal {
			return catalog.BoolValue(false), nil
		}
		if left.IsNull || right.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.BoolValue(left.BoolVal && right.BoolVal), nil

	case lexer.OR:
		// Short-circuit: true OR anything = true
		if !left.IsNull && left.Type == catalog.TypeBool && left.BoolVal {
			return catalog.BoolValue(true), nil
		}
		if !right.IsNull && right.Type == catalog.TypeBool && right.BoolVal {
			return catalog.BoolValue(true), nil
		}
		if left.IsNull || right.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.BoolValue(left.BoolVal || right.BoolVal), nil

	case lexer.LIKE:
		if left.IsNull || right.IsNull {
			return catalog.NullValue(), nil
		}
		matched, err := likeMatch(left.String(), right.StrVal, e.EscapeChar)
		if err != nil {
			return catalog.NullValue(), err
		}
		return catalog.BoolValue(matched), nil

	default:
		// Comparison operators
		if left.IsNull || right.IsNull {
			return catalog.NullValue(), nil
		}
		cmp, err := left.Compare(right)
		if err != nil {
			return catalog.NullValue(), nil // type mismatch → NULL
		}
		var result bool
		switch e.Op.Type {
		case lexer.EQ:
			result = cmp == 0
		case lexer.NEQ:
			result = cmp != 0
		case lexer.LT:
			result = cmp < 0
		case lexer.GT:
			result = cmp > 0
		case lexer.LTE:
			result = cmp <= 0
		case lexer.GTE:
			result = cmp >= 0
		default:
			return catalog.NullValue(), fmt.Errorf("unsupported binary op: %s", e.Op.Literal)
		}
		return catalog.BoolValue(result), nil
	}
}

func evalUnary(e *ast.UnaryExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	val, err := EvalExpr(e.Expr, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	switch e.Op.Type {
	case lexer.NOT:
		if val.IsNull {
			return catalog.NullValue(), nil
		}
		if val.Type != catalog.TypeBool {
			return catalog.NullValue(), fmt.Errorf("NOT requires boolean, got %s", val.Type)
		}
		return catalog.BoolValue(!val.BoolVal), nil
	case lexer.MINUS:
		if val.IsNull {
			return catalog.NullValue(), nil
		}
		switch val.Type {
		case catalog.TypeInt:
			return catalog.IntValue(-val.IntVal), nil
		case catalog.TypeFloat:
			return catalog.FloatValue(-val.FloatVal), nil
		}
		return catalog.NullValue(), fmt.Errorf("unary minus requires numeric, got %s", val.Type)
	}
	return catalog.NullValue(), fmt.Errorf("unsupported unary op: %s", e.Op.Literal)
}

func evalFunction(e *ast.FunctionCall, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	name := strings.ToUpper(e.Name)
	switch name {
	case "COALESCE":
		for _, arg := range e.Args {
			v, err := EvalExpr(arg, tuple, ctx)
			if err != nil {
				return catalog.NullValue(), err
			}
			if !v.IsNull {
				return v, nil
			}
		}
		return catalog.NullValue(), nil

	case "NULLIF":
		if len(e.Args) != 2 {
			return catalog.NullValue(), fmt.Errorf("NULLIF requires 2 arguments")
		}
		v1, _ := EvalExpr(e.Args[0], tuple, ctx)
		v2, _ := EvalExpr(e.Args[1], tuple, ctx)
		cmp, err := v1.Compare(v2)
		if err == nil && cmp == 0 {
			return catalog.NullValue(), nil
		}
		return v1, nil

	case "UPPER":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.ToUpper(v.String())), nil

	case "LOWER":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.ToLower(v.String())), nil

	case "LENGTH", "LEN":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.IntValue(int64(utf8.RuneCountInString(v.String()))), nil

	case "ABS":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		if v.Type == catalog.TypeInt {
			if v.IntVal < 0 {
				return catalog.IntValue(-v.IntVal), nil
			}
			return v, nil
		}
		if v.Type == catalog.TypeFloat {
			if v.FloatVal < 0 {
				return catalog.FloatValue(-v.FloatVal), nil
			}
			return v, nil
		}
		return catalog.NullValue(), nil

	case "TRIM":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.TrimSpace(v.String())), nil

	case "LTRIM":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.TrimLeft(v.StrVal, " \t\r\n")), nil

	case "RTRIM":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.TrimRight(v.StrVal, " \t\r\n")), nil

	case "SUBSTR", "SUBSTRING":
		if len(e.Args) < 2 {
			return catalog.NullValue(), nil
		}
		sv, _ := EvalExpr(e.Args[0], tuple, ctx)
		startV, _ := EvalExpr(e.Args[1], tuple, ctx)
		if sv.IsNull || startV.IsNull {
			return catalog.NullValue(), nil
		}
		// Work in runes so multi-byte Unicode is handled correctly.
		runes := []rune(sv.StrVal)
		start := int(startV.IntVal) - 1 // SQL is 1-based
		if start < 0 {
			start = 0
		}
		if start >= len(runes) {
			return catalog.TextValue(""), nil
		}
		if len(e.Args) >= 3 {
			lenV, _ := EvalExpr(e.Args[2], tuple, ctx)
			if !lenV.IsNull {
				length := int(lenV.IntVal)
				if length < 0 {
					// SQL standard: negative length → empty string
					return catalog.TextValue(""), nil
				}
				end := start + length
				if end > len(runes) {
					end = len(runes)
				}
				return catalog.TextValue(string(runes[start:end])), nil
			}
		}
		return catalog.TextValue(string(runes[start:])), nil

	case "REPLACE":
		if len(e.Args) != 3 {
			return catalog.NullValue(), nil
		}
		sv, _ := EvalExpr(e.Args[0], tuple, ctx)
		fromV, _ := EvalExpr(e.Args[1], tuple, ctx)
		toV, _ := EvalExpr(e.Args[2], tuple, ctx)
		if sv.IsNull || fromV.IsNull || toV.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.TextValue(strings.ReplaceAll(sv.StrVal, fromV.StrVal, toV.StrVal)), nil

	case "ROUND":
		if len(e.Args) < 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		f := toFloat(v)
		if len(e.Args) >= 2 {
			precV, _ := EvalExpr(e.Args[1], tuple, ctx)
			if !precV.IsNull {
				prec := int(precV.IntVal)
				factor := math.Pow(10, float64(prec))
				f = math.Round(f*factor) / factor
				return catalog.FloatValue(f), nil
			}
		}
		return catalog.FloatValue(math.Round(f)), nil

	case "FLOOR":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.FloatValue(math.Floor(toFloat(v))), nil

	case "CEIL", "CEILING":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.FloatValue(math.Ceil(toFloat(v))), nil

	case "POWER", "POW":
		if len(e.Args) != 2 {
			return catalog.NullValue(), nil
		}
		base, _ := EvalExpr(e.Args[0], tuple, ctx)
		exp, _ := EvalExpr(e.Args[1], tuple, ctx)
		if base.IsNull || exp.IsNull {
			return catalog.NullValue(), nil
		}
		return catalog.FloatValue(math.Pow(toFloat(base), toFloat(exp))), nil

	case "SQRT":
		if len(e.Args) != 1 {
			return catalog.NullValue(), nil
		}
		v, _ := EvalExpr(e.Args[0], tuple, ctx)
		if v.IsNull {
			return catalog.NullValue(), nil
		}
		f := toFloat(v)
		if f < 0 {
			return catalog.NullValue(), fmt.Errorf("SQRT of negative number")
		}
		return catalog.FloatValue(math.Sqrt(f)), nil

	// Aggregate functions should not normally reach here in aggregation context
	case "COUNT", "SUM", "AVG", "MIN", "MAX",
		"STDDEV", "STDDEV_POP", "STDDEV_SAMP", "VAR_POP", "VAR_SAMP", "VARIANCE":
		return catalog.NullValue(), fmt.Errorf("aggregate function %s cannot be used as scalar", name)

	case "CONCAT_WS":
		// CONCAT_WS(sep, str1, str2, ...) — NULL separator yields NULL, NULL parts are skipped
		if len(e.Args) < 2 {
			return catalog.NullValue(), nil
		}
		sep, err := EvalExpr(e.Args[0], tuple, ctx)
		if err != nil || sep.IsNull {
			return catalog.NullValue(), err
		}
		var parts []string
		for _, arg := range e.Args[1:] {
			v, _ := EvalExpr(arg, tuple, ctx)
			if !v.IsNull {
				parts = append(parts, v.String())
			}
		}
		return catalog.TextValue(strings.Join(parts, sep.StrVal)), nil

	case "NOW", "CURRENT_TIMESTAMP", "CURRENT_DATE",
		"YEAR", "MONTH", "DAY", "HOUR", "MINUTE", "SECOND",
		"DATE_TRUNC", "DATEDIFF", "DATE_ADD", "DATE_SUB",
		"LOG", "LOG10", "LOG2", "LN", "EXP", "TRUNC", "TRUNCATE",
		"SIGN", "PI", "MOD", "SIN", "COS", "TAN",
		"INSTR", "POSITION", "LPAD", "RPAD", "REVERSE":
		// Evaluate arguments
		argVals := make([]catalog.Value, len(e.Args))
		for i, arg := range e.Args {
			v, err := EvalExpr(arg, tuple, ctx)
			if err != nil {
				return catalog.NullValue(), err
			}
			argVals[i] = v
		}
		if v, handled, err := evalBuiltins(name, argVals); handled {
			return v, err
		}
	}

	// Final fallback: try evalBuiltins for any unrecognised function name
	argVals := make([]catalog.Value, len(e.Args))
	for i, arg := range e.Args {
		v, err := EvalExpr(arg, tuple, ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
		argVals[i] = v
	}
	if v, handled, err := evalBuiltins(name, argVals); handled {
		return v, err
	}

	return catalog.NullValue(), fmt.Errorf("unknown function: %s", e.Name)
}

// toFloat converts an int, float, bool, or text Value to float64.
func toFloat(v catalog.Value) float64 {
	switch v.Type {
	case catalog.TypeInt:
		return float64(v.IntVal)
	case catalog.TypeBool:
		if v.BoolVal {
			return 1.0
		}
		return 0.0
	case catalog.TypeText:
		if f, err := strconv.ParseFloat(v.StrVal, 64); err == nil {
			return f
		}
		return 0.0
	default:
		return v.FloatVal
	}
}

func evalCase(e *ast.CaseExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	// Simple CASE: CASE operand WHEN val THEN result ...
	if e.Operand != nil {
		operand, err := EvalExpr(e.Operand, tuple, ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
		for _, w := range e.Whens {
			cond, err := EvalExpr(w.Condition, tuple, ctx)
			if err != nil {
				return catalog.NullValue(), err
			}
			cmp, err := operand.Compare(cond)
			if err == nil && cmp == 0 {
				return EvalExpr(w.Result, tuple, ctx)
			}
		}
	} else {
		// Searched CASE: CASE WHEN condition THEN result ...
		for _, w := range e.Whens {
			cond, err := EvalExpr(w.Condition, tuple, ctx)
			if err != nil {
				return catalog.NullValue(), err
			}
			if !cond.IsNull && cond.Type == catalog.TypeBool && cond.BoolVal {
				return EvalExpr(w.Result, tuple, ctx)
			}
		}
	}
	if e.ElseExpr != nil {
		return EvalExpr(e.ElseExpr, tuple, ctx)
	}
	return catalog.NullValue(), nil
}

func evalIn(e *ast.InExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	val, err := EvalExpr(e.Expr, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	if val.IsNull {
		return catalog.NullValue(), nil
	}

	// Subquery form: IN (SELECT ...)
	if e.Subquery != nil {
		rows, err := runSubquery(e.Subquery, tuple, ctx)
		if err != nil {
			return catalog.NullValue(), err
		}
		found := false
		seenNullSQ := false
		for _, row := range rows {
			if len(row.Values) == 0 {
				seenNullSQ = true
				continue
			}
			rv := row.Values[0]
			if rv.IsNull {
				seenNullSQ = true
				continue
			}
			cmp, err := val.Compare(rv)
			if err == nil && cmp == 0 {
				found = true
				break
			}
		}
		if found {
			return catalog.BoolValue(!e.Negated), nil
		}
		if seenNullSQ {
			return catalog.NullValue(), nil
		}
		return catalog.BoolValue(e.Negated), nil
	}

	// List form: IN (v1, v2, ...)
	found := false
	seenNull := false
	for _, item := range e.List {
		itemVal, err := EvalExpr(item, tuple, ctx)
		if err != nil {
			continue
		}
		if itemVal.IsNull {
			seenNull = true
			continue
		}
		cmp, err := val.Compare(itemVal)
		if err == nil && cmp == 0 {
			found = true
			break
		}
	}

	if found {
		// Definite match: IN → TRUE, NOT IN → FALSE
		return catalog.BoolValue(!e.Negated), nil
	}
	if seenNull {
		// x IN (..., NULL, ...) where x doesn't match any non-null element → NULL (unknown)
		// x NOT IN (..., NULL, ...) where x doesn't match any non-null element → NULL (unknown)
		return catalog.NullValue(), nil
	}
	return catalog.BoolValue(e.Negated), nil
}

func evalBetween(e *ast.BetweenExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	val, err := EvalExpr(e.Expr, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	low, err := EvalExpr(e.Low, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	high, err := EvalExpr(e.High, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	if val.IsNull || low.IsNull || high.IsNull {
		return catalog.NullValue(), nil
	}

	cmpLow, err := val.Compare(low)
	if err != nil {
		return catalog.NullValue(), nil
	}
	cmpHigh, err := val.Compare(high)
	if err != nil {
		return catalog.NullValue(), nil
	}

	inRange := cmpLow >= 0 && cmpHigh <= 0
	if e.Negated {
		inRange = !inRange
	}
	return catalog.BoolValue(inRange), nil
}

// evalExists evaluates EXISTS (subquery) — returns true if the subquery yields any row.
func evalExists(e *ast.ExistsExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	rows, err := runSubquery(e.Subquery, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	exists := len(rows) > 0
	if e.Negated {
		exists = !exists
	}
	return catalog.BoolValue(exists), nil
}

// evalSubqueryScalar evaluates (SELECT ...) used as a scalar expression.
// It must return exactly one row and one column; otherwise an error is returned.
func evalSubqueryScalar(e *ast.SubqueryExpr, tuple *exectypes.Tuple, ctx *exectypes.ExecContext) (catalog.Value, error) {
	rows, err := runSubquery(e.Select, tuple, ctx)
	if err != nil {
		return catalog.NullValue(), err
	}
	if len(rows) == 0 {
		return catalog.NullValue(), nil // scalar subquery with no rows → NULL
	}
	if len(rows) > 1 {
		return catalog.NullValue(), fmt.Errorf("scalar subquery returns more than one row")
	}
	if len(rows[0].Values) == 0 {
		return catalog.NullValue(), nil
	}
	return rows[0].Values[0], nil
}

// runSubquery executes a SELECT subquery via the SubqueryRunner stored in ctx.
func runSubquery(sel *ast.SelectStatement, outerTuple *exectypes.Tuple, ctx *exectypes.ExecContext) ([]exectypes.Tuple, error) {
	if ctx == nil || ctx.Runner == nil {
		return nil, fmt.Errorf("subquery evaluation is not supported in this context")
	}
	if ctx.SubqueryDepth >= maxSubqueryDepth {
		return nil, fmt.Errorf("subquery nesting depth exceeds limit of %d", maxSubqueryDepth)
	}
	return ctx.Runner.RunSelect(sel, outerTuple)
}

// likeMatch converts a SQL LIKE pattern to a regex and matches it.
// escapeChar (0 or 1 character) is the ESCAPE character; empty string means no escape.
// Compiled patterns are cached in likeCache (escape-char is part of the cache key).
func likeMatch(str, pattern, escapeChar string) (bool, error) {
	cacheKey := pattern + "\x00" + escapeChar
	if cached, ok := likeCache.Load(cacheKey); ok {
		return cached.(*regexp.Regexp).MatchString(str), nil
	}

	// Convert SQL LIKE to regex: % → .*, _ → .
	// SQL LIKE is case-sensitive by default; no (?i) flag.
	var sb strings.Builder
	sb.WriteString("^")
	runes := []rune(pattern)
	var escRune rune
	hasEscape := len(escapeChar) == 1
	if hasEscape {
		escRune = []rune(escapeChar)[0]
	}
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if hasEscape && ch == escRune {
			i++ // consume escape char; next char is literal
			if i < len(runes) {
				sb.WriteString(regexp.QuoteMeta(string(runes[i])))
			}
			continue
		}
		switch ch {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	sb.WriteString("$")

	re, err := regexp.Compile(sb.String())
	if err != nil {
		return false, fmt.Errorf("invalid LIKE pattern: %q", pattern)
	}
	if atomic.LoadInt64(&likeCacheSize) < maxLikeCacheSize {
		likeCache.Store(cacheKey, re)
		atomic.AddInt64(&likeCacheSize, 1)
	}
	return re.MatchString(str), nil
}

// IsTruthy returns true if a value is a truthy boolean.
func IsTruthy(v catalog.Value) bool {
	return !v.IsNull && v.Type == catalog.TypeBool && v.BoolVal
}

// -----------------------------------------------------------------------
// Date / Time helpers
// -----------------------------------------------------------------------

var dateFmts = []string{
	"2006-01-02",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, f := range dateFmts {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %q", s)
}

func extractDatePart(part string, v catalog.Value) (catalog.Value, error) {
	// Handle numeric epoch specially
	if v.Type == catalog.TypeInt {
		t := time.Unix(v.IntVal, 0).UTC()
		return extractFromTime(part, t)
	}
	if v.Type == catalog.TypeFloat {
		t := time.Unix(int64(v.FloatVal), 0).UTC()
		return extractFromTime(part, t)
	}
	t, err := parseDate(v.StrVal)
	if err != nil {
		return catalog.NullValue(), err
	}
	return extractFromTime(part, t)
}

func extractFromTime(part string, t time.Time) (catalog.Value, error) {
	switch strings.ToUpper(part) {
	case "YEAR":
		return catalog.FloatValue(float64(t.Year())), nil
	case "MONTH":
		return catalog.FloatValue(float64(t.Month())), nil
	case "DAY":
		return catalog.FloatValue(float64(t.Day())), nil
	case "HOUR":
		return catalog.FloatValue(float64(t.Hour())), nil
	case "MINUTE":
		return catalog.FloatValue(float64(t.Minute())), nil
	case "SECOND":
		return catalog.FloatValue(float64(t.Second())), nil
	case "DOW": // 0=Sunday
		return catalog.FloatValue(float64(t.Weekday())), nil
	case "DOY":
		return catalog.FloatValue(float64(t.YearDay())), nil
	case "EPOCH":
		return catalog.FloatValue(float64(t.Unix())), nil
	case "QUARTER":
		return catalog.FloatValue(float64((t.Month()-1)/3 + 1)), nil
	case "WEEK":
		_, week := t.ISOWeek()
		return catalog.FloatValue(float64(week)), nil
	default:
		return catalog.NullValue(), fmt.Errorf("unknown EXTRACT part: %q", part)
	}
}

// -----------------------------------------------------------------------
// New built-in functions (date, math, string)
// -----------------------------------------------------------------------

func evalBuiltins(name string, args []catalog.Value) (catalog.Value, bool, error) {
	for _, a := range args {
		if a.IsNull {
			return catalog.NullValue(), true, nil
		}
	}
	switch name {
	// --- Date functions ---
	case "NOW", "CURRENT_TIMESTAMP":
		return catalog.TextValue(time.Now().UTC().Format("2006-01-02 15:04:05")), true, nil
	case "CURRENT_DATE":
		return catalog.TextValue(time.Now().UTC().Format("2006-01-02")), true, nil
	case "YEAR":
		if len(args) == 1 {
			v, err := extractDatePart("YEAR", args[0])
			return v, true, err
		}
	case "MONTH":
		if len(args) == 1 {
			v, err := extractDatePart("MONTH", args[0])
			return v, true, err
		}
	case "DAY":
		if len(args) == 1 {
			v, err := extractDatePart("DAY", args[0])
			return v, true, err
		}
	case "HOUR":
		if len(args) == 1 {
			v, err := extractDatePart("HOUR", args[0])
			return v, true, err
		}
	case "MINUTE":
		if len(args) == 1 {
			v, err := extractDatePart("MINUTE", args[0])
			return v, true, err
		}
	case "SECOND":
		if len(args) == 1 {
			v, err := extractDatePart("SECOND", args[0])
			return v, true, err
		}
	case "DATE_TRUNC":
		if len(args) == 2 {
			part := args[0].StrVal
			t, err := parseDate(args[1].StrVal)
			if err != nil {
				return catalog.NullValue(), true, err
			}
			var truncated time.Time
			switch strings.ToUpper(part) {
			case "YEAR":
				truncated = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
			case "MONTH":
				truncated = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
			case "DAY":
				truncated = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			case "HOUR":
				truncated = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
			case "WEEK":
				// truncate to Monday
				wd := int(t.Weekday())
				if wd == 0 {
					wd = 7
				}
				truncated = t.AddDate(0, 0, -(wd - 1))
				truncated = time.Date(truncated.Year(), truncated.Month(), truncated.Day(), 0, 0, 0, 0, t.Location())
			default:
				return catalog.NullValue(), true, fmt.Errorf("DATE_TRUNC: unknown part %q", part)
			}
			return catalog.TextValue(truncated.Format("2006-01-02 15:04:05")), true, nil
		}
	case "DATEDIFF":
		if len(args) == 2 {
			t1, err1 := parseDate(args[0].StrVal)
			t2, err2 := parseDate(args[1].StrVal)
			if err1 != nil || err2 != nil {
				return catalog.NullValue(), true, nil
			}
			diff := t1.Sub(t2).Hours() / 24
			return catalog.FloatValue(diff), true, nil
		}
	case "DATE_ADD":
		if len(args) == 2 {
			t, err := parseDate(args[0].StrVal)
			if err != nil {
				return catalog.NullValue(), true, err
			}
			days := int(args[1].IntVal)
			result := t.AddDate(0, 0, days)
			return catalog.TextValue(result.Format("2006-01-02")), true, nil
		}
	case "DATE_SUB":
		if len(args) == 2 {
			t, err := parseDate(args[0].StrVal)
			if err != nil {
				return catalog.NullValue(), true, err
			}
			days := int(args[1].IntVal)
			result := t.AddDate(0, 0, -days)
			return catalog.TextValue(result.Format("2006-01-02")), true, nil
		}

	// --- Advanced math ---
	case "LOG":
		if len(args) == 1 {
			return catalog.FloatValue(math.Log10(toFloat(args[0]))), true, nil
		}
		if len(args) == 2 {
			base := toFloat(args[0])
			x := toFloat(args[1])
			if base <= 0 || base == 1 || x <= 0 {
				return catalog.NullValue(), true, nil
			}
			return catalog.FloatValue(math.Log(x) / math.Log(base)), true, nil
		}
	case "LOG10":
		if len(args) == 1 {
			return catalog.FloatValue(math.Log10(toFloat(args[0]))), true, nil
		}
	case "LOG2":
		if len(args) == 1 {
			return catalog.FloatValue(math.Log2(toFloat(args[0]))), true, nil
		}
	case "LN":
		if len(args) == 1 {
			return catalog.FloatValue(math.Log(toFloat(args[0]))), true, nil
		}
	case "EXP":
		if len(args) == 1 {
			return catalog.FloatValue(math.Exp(toFloat(args[0]))), true, nil
		}
	case "TRUNC", "TRUNCATE":
		if len(args) == 1 {
			return catalog.FloatValue(math.Trunc(toFloat(args[0]))), true, nil
		}
		if len(args) == 2 {
			scale := math.Pow(10, toFloat(args[1]))
			return catalog.FloatValue(math.Trunc(toFloat(args[0])*scale) / scale), true, nil
		}
	case "SIGN":
		if len(args) == 1 {
			x := toFloat(args[0])
			if x > 0 {
				return catalog.FloatValue(1), true, nil
			} else if x < 0 {
				return catalog.FloatValue(-1), true, nil
			}
			return catalog.FloatValue(0), true, nil
		}
	case "PI":
		return catalog.FloatValue(math.Pi), true, nil
	case "MOD":
		if len(args) == 2 {
			a, b := args[0], args[1]
			bv := toFloat(b)
			if bv == 0 {
				return catalog.NullValue(), true, fmt.Errorf("modulo by zero")
			}
			return catalog.FloatValue(math.Mod(toFloat(a), bv)), true, nil
		}
	case "SIN":
		if len(args) == 1 {
			return catalog.FloatValue(math.Sin(toFloat(args[0]))), true, nil
		}
	case "COS":
		if len(args) == 1 {
			return catalog.FloatValue(math.Cos(toFloat(args[0]))), true, nil
		}
	case "TAN":
		if len(args) == 1 {
			return catalog.FloatValue(math.Tan(toFloat(args[0]))), true, nil
		}

	// --- Advanced string functions ---
	case "INSTR":
		if len(args) == 2 {
			s, sub := args[0].StrVal, args[1].StrVal
			idx := strings.Index(s, sub)
			if idx < 0 {
				return catalog.IntValue(0), true, nil
			}
			// Return 1-based rune position
			runeIdx := utf8.RuneCountInString(s[:idx]) + 1
			return catalog.IntValue(int64(runeIdx)), true, nil
		}
	case "POSITION": // POSITION(sub, str) — positional args form
		if len(args) == 2 {
			s, sub := args[1].StrVal, args[0].StrVal
			idx := strings.Index(s, sub)
			if idx < 0 {
				return catalog.IntValue(0), true, nil
			}
			return catalog.IntValue(int64(utf8.RuneCountInString(s[:idx]) + 1)), true, nil
		}
	case "LPAD":
		if len(args) >= 2 {
			s := args[0].StrVal
			targetLen := int(args[1].IntVal)
			pad := " "
			if len(args) >= 3 {
				pad = args[2].StrVal
			}
			runes := []rune(s)
			if len(runes) >= targetLen {
				// Truncate: return the first targetLen runes
				return catalog.TextValue(string(runes[:targetLen])), true, nil
			}
			if pad == "" {
				return catalog.TextValue(s), true, nil
			}
			for len(runes) < targetLen {
				runes = append([]rune(pad), runes...)
			}
			// After padding, take the rightmost targetLen runes
			return catalog.TextValue(string(runes[len(runes)-targetLen:])), true, nil
		}
	case "RPAD":
		if len(args) >= 2 {
			s := args[0].StrVal
			targetLen := int(args[1].IntVal)
			pad := " "
			if len(args) >= 3 {
				pad = args[2].StrVal
			}
			if pad == "" {
				return catalog.TextValue(s), true, nil
			}
			runes := []rune(s)
			for len(runes) < targetLen {
				runes = append(runes, []rune(pad)...)
			}
			return catalog.TextValue(string(runes[:targetLen])), true, nil
		}
	case "REVERSE":
		if len(args) == 1 {
			runes := []rune(args[0].StrVal)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return catalog.TextValue(string(runes)), true, nil
		}
	}
	return catalog.NullValue(), false, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
