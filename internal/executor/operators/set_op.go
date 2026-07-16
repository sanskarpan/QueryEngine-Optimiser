package operators

import (
	"fmt"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// SetOpOp implements UNION [ALL], INTERSECT [ALL], EXCEPT [ALL].
// It materialises both sides then computes the result set.
type SetOpOp struct {
	Op    string // "UNION", "INTERSECT", "EXCEPT"
	All   bool
	Left  Operator
	Right Operator

	ctx    *exectypes.ExecContext
	result []exectypes.Tuple
	pos    int
	schema []catalog.Column
}

func (op *SetOpOp) Schema() []catalog.Column { return op.schema }

func (op *SetOpOp) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx

	if err := op.Left.Open(ctx); err != nil {
		return err
	}
	if err := op.Right.Open(ctx); err != nil {
		return err
	}

	leftSchema := op.Left.Schema()
	rightSchema := op.Right.Schema()
	if len(leftSchema) != len(rightSchema) {
		return fmt.Errorf("%s: left side has %d columns, right side has %d columns",
			op.Op, len(leftSchema), len(rightSchema))
	}
	op.schema = leftSchema

	var leftRows, rightRows []exectypes.Tuple
	for {
		t, err := op.Left.Next()
		if err != nil {
			return err
		}
		if t == nil {
			break
		}
		leftRows = append(leftRows, *t)
	}
	op.Left.Close()

	for {
		t, err := op.Right.Next()
		if err != nil {
			return err
		}
		if t == nil {
			break
		}
		rightRows = append(rightRows, *t)
	}
	op.Right.Close()

	switch op.Op {
	case "UNION":
		op.result = setUnion(leftRows, rightRows, op.All)
	case "INTERSECT":
		op.result = setIntersect(leftRows, rightRows, op.All)
	case "EXCEPT":
		op.result = setExcept(leftRows, rightRows, op.All)
	}

	op.pos = 0
	return nil
}

func (op *SetOpOp) Next() (*exectypes.Tuple, error) {
	if op.pos >= len(op.result) {
		return nil, nil
	}
	row := op.result[op.pos]
	op.pos++
	return &row, nil
}

// -----------------------------------------------------------------------
// DedupeOp — streaming deduplication for SELECT DISTINCT.
// -----------------------------------------------------------------------

type DedupeOp struct {
	Child  Operator
	seen   map[string]struct{}
	schema []catalog.Column
}

func (op *DedupeOp) Schema() []catalog.Column { return op.schema }

func (op *DedupeOp) Open(ctx *exectypes.ExecContext) error {
	if err := op.Child.Open(ctx); err != nil {
		return err
	}
	op.schema = op.Child.Schema()
	op.seen = make(map[string]struct{})
	return nil
}

func (op *DedupeOp) Next() (*exectypes.Tuple, error) {
	for {
		tuple, err := op.Child.Next()
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			return nil, nil
		}
		key := tupleKey(*tuple)
		if _, dup := op.seen[key]; dup {
			continue
		}
		op.seen[key] = struct{}{}
		return tuple, nil
	}
}

func (op *DedupeOp) Close() error {
	op.seen = nil
	return op.Child.Close()
}

func (op *SetOpOp) Close() error {
	op.result = nil
	return nil
}

// tupleKey returns a string key for deduplication.
func tupleKey(t exectypes.Tuple) string {
	key := ""
	for _, v := range t.Values {
		key += v.String() + "\x00"
	}
	return key
}

func setUnion(left, right []exectypes.Tuple, all bool) []exectypes.Tuple {
	if all {
		return append(left, right...)
	}
	seen := make(map[string]struct{})
	var out []exectypes.Tuple
	for _, t := range append(left, right...) {
		k := tupleKey(t)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func setIntersect(left, right []exectypes.Tuple, all bool) []exectypes.Tuple {
	if all {
		// Count-aware intersect: min of counts from each side
		rightCounts := make(map[string]int)
		for _, t := range right {
			rightCounts[tupleKey(t)]++
		}
		usedCounts := make(map[string]int)
		var out []exectypes.Tuple
		for _, t := range left {
			k := tupleKey(t)
			if usedCounts[k] < rightCounts[k] {
				usedCounts[k]++
				out = append(out, t)
			}
		}
		return out
	}
	rightSet := make(map[string]struct{})
	for _, t := range right {
		rightSet[tupleKey(t)] = struct{}{}
	}
	seen := make(map[string]struct{})
	var out []exectypes.Tuple
	for _, t := range left {
		k := tupleKey(t)
		if _, inRight := rightSet[k]; inRight {
			if _, emitted := seen[k]; !emitted {
				seen[k] = struct{}{}
				out = append(out, t)
			}
		}
	}
	return out
}

func setExcept(left, right []exectypes.Tuple, all bool) []exectypes.Tuple {
	if all {
		// Count-aware: emit max(leftCount - rightCount, 0) copies.
		rightCounts := make(map[string]int)
		for _, t := range right {
			rightCounts[tupleKey(t)]++
		}
		var out []exectypes.Tuple
		for _, t := range left {
			k := tupleKey(t)
			if rightCounts[k] > 0 {
				rightCounts[k]-- // consume one right-side match
			} else {
				out = append(out, t)
			}
		}
		return out
	}
	rightSet := make(map[string]struct{})
	for _, t := range right {
		rightSet[tupleKey(t)] = struct{}{}
	}
	seen := make(map[string]struct{})
	var out []exectypes.Tuple
	for _, t := range left {
		k := tupleKey(t)
		if _, inRight := rightSet[k]; !inRight {
			if _, emitted := seen[k]; !emitted {
				seen[k] = struct{}{}
				out = append(out, t)
			}
		}
	}
	return out
}
