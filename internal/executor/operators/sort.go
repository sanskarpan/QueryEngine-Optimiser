package operators

import (
	"sort"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// SortSpec specifies a single ORDER BY expression with direction and null ordering.
type SortSpec struct {
	Expr           ast.Expression
	Ascending      bool
	NullsFirst     bool
	NullsSpecified bool
}

// Sort materializes all input and sorts in-memory.
type Sort struct {
	Child     Operator
	SortSpecs []SortSpec

	ctx    *exectypes.ExecContext
	rows   []exectypes.Tuple
	pos    int
	schema []catalog.Column
}

func (op *Sort) Schema() []catalog.Column { return op.schema }

func (op *Sort) Open(ctx *exectypes.ExecContext) error {
	op.ctx = ctx
	ctx.SortOps++

	if err := op.Child.Open(ctx); err != nil {
		return err
	}
	op.schema = op.Child.Schema()

	for {
		tuple, err := op.Child.Next()
		if err != nil {
			return err
		}
		if tuple == nil {
			break
		}
		op.rows = append(op.rows, *tuple)
	}
	op.Child.Close()

	sort.SliceStable(op.rows, func(i, j int) bool {
		for _, spec := range op.SortSpecs {
			vi, erri := EvalExpr(spec.Expr, &op.rows[i], op.ctx)
			vj, errj := EvalExpr(spec.Expr, &op.rows[j], op.ctx)
			if erri != nil || errj != nil {
				continue
			}
			// Handle NULLs: default is NULLS LAST for ASC, NULLS FIRST for DESC.
			iNull := vi.IsNull
			jNull := vj.IsNull
			if iNull || jNull {
				if iNull && jNull {
					continue
				}
				nullsFirst := spec.NullsFirst
				if !spec.NullsSpecified {
					// SQL default: ASC → NULLS LAST, DESC → NULLS FIRST
					nullsFirst = !spec.Ascending
				}
				if iNull {
					return nullsFirst
				}
				return !nullsFirst
			}
			cmp, err := vi.Compare(vj)
			if err != nil {
				continue
			}
			if cmp == 0 {
				continue // tie-break with next spec
			}
			if spec.Ascending {
				return cmp < 0
			}
			return cmp > 0
		}
		return false
	})

	op.pos = 0
	return nil
}

func (op *Sort) Next() (*exectypes.Tuple, error) {
	if op.pos >= len(op.rows) {
		return nil, nil
	}
	row := op.rows[op.pos]
	op.pos++
	return &row, nil
}

func (op *Sort) Close() error {
	op.rows = nil
	return nil
}
