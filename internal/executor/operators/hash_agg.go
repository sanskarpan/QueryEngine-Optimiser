package operators

import (
	"fmt"
	"math"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// AggExpr describes a single aggregate computation.
type AggExpr struct {
	Function string         // COUNT, SUM, AVG, MIN, MAX
	Arg      ast.Expression // nil for COUNT(*)
	StarArg  bool
	Distinct bool // COUNT(DISTINCT col)
	Alias    string
}

// aggState holds the running state for one aggregate.
type aggState struct {
	fn       string
	distinct bool
	seen     map[string]struct{} // for DISTINCT aggregates
	count    int64
	sum      float64
	sumSq    float64 // sum of squares, for variance/stddev
	min      catalog.Value
	max      catalog.Value
}

func newAggState(fn string, distinct bool) *aggState {
	s := &aggState{
		fn:       strings.ToUpper(fn),
		distinct: distinct,
		min:      catalog.NullValue(),
		max:      catalog.NullValue(),
	}
	if distinct {
		s.seen = make(map[string]struct{})
	}
	return s
}

func (s *aggState) accumulate(val catalog.Value) {
	if val.IsNull {
		return
	}
	// For DISTINCT aggregates, skip duplicate values.
	if s.distinct {
		key := val.String()
		if _, already := s.seen[key]; already {
			return
		}
		s.seen[key] = struct{}{}
	}
	// Only accumulate numeric values into sum (SUM/AVG); skip TEXT and BOOL to avoid NaN.
	if val.Type == catalog.TypeInt || val.Type == catalog.TypeFloat {
		x := toFloat(val)
		s.sum += x
		s.sumSq += x * x
	}
	s.count++

	if s.min.IsNull {
		s.min = val
		s.max = val
		return
	}
	if cmp, err := val.Compare(s.min); err == nil && cmp < 0 {
		s.min = val
	}
	if cmp, err := val.Compare(s.max); err == nil && cmp > 0 {
		s.max = val
	}
}

func (s *aggState) result() catalog.Value {
	switch s.fn {
	case "COUNT":
		return catalog.IntValue(s.count)
	case "SUM":
		if s.count == 0 {
			return catalog.NullValue()
		}
		return catalog.FloatValue(s.sum)
	case "AVG":
		if s.count == 0 {
			return catalog.NullValue()
		}
		return catalog.FloatValue(s.sum / float64(s.count))
	case "MIN":
		return s.min
	case "MAX":
		return s.max
	case "VAR_POP", "VARIANCE":
		if s.count == 0 {
			return catalog.NullValue()
		}
		mean := s.sum / float64(s.count)
		return catalog.FloatValue(s.sumSq/float64(s.count) - mean*mean)
	case "VAR_SAMP":
		if s.count < 2 {
			return catalog.NullValue()
		}
		mean := s.sum / float64(s.count)
		return catalog.FloatValue((s.sumSq - float64(s.count)*mean*mean) / float64(s.count-1))
	case "STDDEV_POP":
		if s.count == 0 {
			return catalog.NullValue()
		}
		mean := s.sum / float64(s.count)
		v := s.sumSq/float64(s.count) - mean*mean
		if v < 0 {
			v = 0
		}
		return catalog.FloatValue(math.Sqrt(v))
	case "STDDEV", "STDDEV_SAMP":
		if s.count < 2 {
			return catalog.NullValue()
		}
		mean := s.sum / float64(s.count)
		v := (s.sumSq - float64(s.count)*mean*mean) / float64(s.count-1)
		if v < 0 {
			v = 0
		}
		return catalog.FloatValue(math.Sqrt(v))
	}
	return catalog.NullValue()
}

// groupKey holds per-group aggregate states.
type groupKey struct {
	keyStr string
	vals   []catalog.Value // group-by values
	states []*aggState
}

// HashAggregate implements hash-based aggregation.
type HashAggregate struct {
	Child   Operator
	GroupBy []ast.Expression
	Aggs    []AggExpr

	ctx     *exectypes.ExecContext
	groups  map[string]*groupKey
	order   []string // insertion order for deterministic output
	output  []*groupKey
	outPos  int
	schema  []catalog.Column
}

func (op *HashAggregate) Schema() []catalog.Column { return op.schema }

func (op *HashAggregate) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	if err := op.Child.Open(ctx); err != nil {
		return err
	}

	childSchema := op.Child.Schema()
	op.groups = make(map[string]*groupKey)

	// First pass: consume all input and build hash groups
	for {
		tuple, err := op.Child.Next()
		if err != nil {
			return err
		}
		if tuple == nil {
			break
		}

		// Build group key — use type-tagged strings so NULL != text('NULL') != int(1), etc.
		groupVals := make([]catalog.Value, len(op.GroupBy))
		var keyParts []string
		for i, g := range op.GroupBy {
			v, err := EvalExpr(g, tuple, op.ctx)
			if err != nil {
				return err
			}
			groupVals[i] = v
			keyParts = append(keyParts, typedGroupKey(v))
		}
		keyStr := strings.Join(keyParts, "\x01")

		gk, exists := op.groups[keyStr]
		if !exists {
			states := make([]*aggState, len(op.Aggs))
			for i, agg := range op.Aggs {
				states[i] = newAggState(agg.Function, agg.Distinct)
			}
			gk = &groupKey{keyStr: keyStr, vals: groupVals, states: states}
			op.groups[keyStr] = gk
			op.order = append(op.order, keyStr)
		}

		// Accumulate aggregates
		for i, agg := range op.Aggs {
			if agg.StarArg || agg.Arg == nil {
				// COUNT(*): accumulate 1
				gk.states[i].count++
			} else {
				v, err := EvalExpr(agg.Arg, tuple, op.ctx)
				if err != nil {
					continue
				}
				gk.states[i].accumulate(v)
			}
		}
	}
	op.Child.Close()

	// Build output slice
	op.output = make([]*groupKey, len(op.order))
	for i, k := range op.order {
		op.output[i] = op.groups[k]
	}

	// Build schema
	var cols []catalog.Column
	for i, g := range op.GroupBy {
		name := exprName(g)
		dt := inferColumnType(g, childSchema)
		cols = append(cols, catalog.Column{Name: name, Type: dt, Index: i})
	}
	for i, agg := range op.Aggs {
		name := agg.Alias
		if name == "" {
			if agg.StarArg {
				name = fmt.Sprintf("%s(*)", agg.Function)
			} else {
				name = fmt.Sprintf("%s(%s)", agg.Function, ast.PrintExpr(agg.Arg))
			}
		}
		dt := aggResultType(agg.Function, agg.Arg, childSchema)
		cols = append(cols, catalog.Column{Name: name, Type: dt, Index: len(op.GroupBy) + i})
	}
	op.schema = cols
	op.outPos = 0

	return nil
}

func (op *HashAggregate) Next() (*exectypes.Tuple, error) {
	// Handle COUNT(*) with no groups (e.g., SELECT COUNT(*) FROM t — one output row)
	if len(op.GroupBy) == 0 && len(op.output) == 0 && op.outPos == 0 {
		// Emit single row with all aggregates computed over empty group
		states := make([]*aggState, len(op.Aggs))
		for i, agg := range op.Aggs {
			states[i] = newAggState(agg.Function, agg.Distinct)
		}
		op.output = []*groupKey{{states: states}}
		op.order = []string{""}
	}

	if op.outPos >= len(op.output) {
		return nil, nil // EOF
	}

	gk := op.output[op.outPos]
	op.outPos++

	vals := make([]catalog.Value, len(gk.vals)+len(gk.states))
	copy(vals, gk.vals)
	for i, state := range gk.states {
		vals[len(gk.vals)+i] = state.result()
	}
	return &exectypes.Tuple{Values: vals, Schema: op.schema}, nil
}

func (op *HashAggregate) Close() error {
	op.groups = nil
	op.output = nil
	return nil
}

func exprName(expr ast.Expression) string {
	return ast.PrintExpr(expr)
}

func aggResultType(fn string, arg ast.Expression, schema []catalog.Column) catalog.DataType {
	switch strings.ToUpper(fn) {
	case "COUNT":
		return catalog.TypeInt
	case "SUM", "AVG":
		return catalog.TypeFloat
	case "MIN", "MAX":
		if arg != nil {
			if dt := inferColumnType(arg, schema); dt != catalog.TypeNull {
				return dt
			}
		}
		return catalog.TypeNull
	default:
		return catalog.TypeNull
	}
}

// typedGroupKey produces a string that uniquely identifies a value including its type,
// preventing collisions between NULL and the text string 'NULL', or between 0 and false.
func typedGroupKey(v catalog.Value) string {
	if v.IsNull {
		return "\x00" // unique sentinel for NULL
	}
	switch v.Type {
	case catalog.TypeInt:
		return fmt.Sprintf("i\x01%d", v.IntVal)
	case catalog.TypeFloat:
		return fmt.Sprintf("f\x01%g", v.FloatVal)
	case catalog.TypeBool:
		if v.BoolVal {
			return "b\x01true"
		}
		return "b\x01false"
	default: // TypeText
		return "t\x01" + v.StrVal
	}
}
