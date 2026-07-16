package executor

import (
	"fmt"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/executor/operators"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// Result holds the output of query execution.
type Result struct {
	Columns []string
	Rows    [][]catalog.Value
	Schema  []catalog.Column
}

// Execute runs a physical plan and returns the result set.
func Execute(plan physical.Plan, ctx *exectypes.ExecContext) (*Result, error) {
	// Install the subquery runner so expression evaluation can run nested queries.
	if ctx.Runner == nil && ctx.Catalog != nil {
		ctx.Runner = &subqueryRunner{cat: ctx.Catalog, parentCtx: ctx, ctes: ctx.CTEs}
	}

	op, err := buildOperator(plan)
	if err != nil {
		return nil, fmt.Errorf("executor: %w", err)
	}

	if err := op.Open(ctx); err != nil {
		return nil, fmt.Errorf("executor open: %w", err)
	}
	defer op.Close()

	schema := op.Schema()
	result := &Result{Schema: schema}
	for _, col := range schema {
		result.Columns = append(result.Columns, col.Name)
	}

	for {
		// Honour context cancellation / timeout between rows.
		if ctx.Ctx != nil {
			select {
			case <-ctx.Ctx.Done():
				return nil, ctx.Ctx.Err()
			default:
			}
		}

		tuple, err := op.Next()
		if err != nil {
			return nil, fmt.Errorf("executor next: %w", err)
		}
		if tuple == nil {
			break
		}
		result.Rows = append(result.Rows, tuple.Values)
		ctx.RowsProduced++
	}

	return result, nil
}

// subqueryRunner implements exectypes.SubqueryRunner using the full executor pipeline.
type subqueryRunner struct {
	cat       *catalog.Catalog
	parentCtx *exectypes.ExecContext
	ctes      map[string]*ast.SelectStatement // inherited CTE context
}

func (r *subqueryRunner) RunSelect(sel *ast.SelectStatement, outerTuple *exectypes.Tuple) ([]exectypes.Tuple, error) {
	lb := logical.NewBuilder(r.cat)
	// Inject active CTEs so subqueries can reference them
	if len(r.ctes) > 0 {
		lb.InjectCTEs(r.ctes)
	}
	lplan, err := lb.Build(sel)
	if err != nil {
		return nil, fmt.Errorf("subquery plan: %w", err)
	}

	opt := optimizer.New()
	oplan := opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(oplan)
	if err != nil {
		return nil, fmt.Errorf("subquery physical plan: %w", err)
	}

	// Child context shares storage and catalog; runner allows nested subqueries.
	// OuterTuple enables correlated subqueries. Depth is incremented for nesting limit.
	childCtx := exectypes.NewExecContext(r.cat, r.parentCtx.Storage)
	childCtx.Runner = r
	childCtx.OuterTuple = outerTuple
	childCtx.SubqueryDepth = r.parentCtx.SubqueryDepth + 1
	childCtx.CTEs = r.ctes

	op, err := buildOperator(pplan)
	if err != nil {
		return nil, fmt.Errorf("subquery build: %w", err)
	}
	if err := op.Open(childCtx); err != nil {
		return nil, fmt.Errorf("subquery open: %w", err)
	}
	defer op.Close()

	var rows []exectypes.Tuple
	for {
		t, err := op.Next()
		if err != nil {
			return nil, fmt.Errorf("subquery next: %w", err)
		}
		if t == nil {
			break
		}
		rows = append(rows, *t)
	}
	return rows, nil
}

// buildOperator converts a physical plan node to an executable operator.
func buildOperator(plan physical.Plan) (operators.Operator, error) {
	switch n := plan.(type) {
	case *physical.SeqScan:
		return &operators.SeqScan{
			TableName: n.TableName,
			Alias:     n.Alias,
			Table:     n.Table,
		}, nil

	case *physical.Filter:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		return &operators.Filter{Child: child, Predicate: n.Predicate}, nil

	case *physical.Projection:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		return &operators.Projection{
			Child:       child,
			Expressions: n.Expressions,
			Aliases:     n.Aliases,
		}, nil

	case *physical.HashJoin:
		left, err := buildOperator(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := buildOperator(n.Right)
		if err != nil {
			return nil, err
		}
		return &operators.HashJoin{
			Left:      left,
			Right:     right,
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}, nil

	case *physical.NestedLoopJoin:
		left, err := buildOperator(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := buildOperator(n.Right)
		if err != nil {
			return nil, err
		}
		return &operators.NestedLoopJoin{
			Left:      left,
			Right:     right,
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}, nil

	case *physical.SortMergeJoin:
		left, err := buildOperator(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := buildOperator(n.Right)
		if err != nil {
			return nil, err
		}
		return &operators.SortMergeJoin{
			Left:      left,
			Right:     right,
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}, nil

	case *physical.HashAggregate:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		aggs := make([]operators.AggExpr, len(n.Aggs))
		for i, a := range n.Aggs {
			aggs[i] = operators.AggExpr{
				Function: a.Function,
				Arg:      a.Arg,
				StarArg:  a.StarArg,
				Distinct: a.Distinct,
				Alias:    a.Alias,
			}
		}
		return &operators.HashAggregate{
			Child:   child,
			GroupBy: n.GroupBy,
			Aggs:    aggs,
		}, nil

	case *physical.Sort:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		specs := make([]operators.SortSpec, len(n.SortSpecs))
		for i, s := range n.SortSpecs {
			specs[i] = operators.SortSpec{
				Expr:           s.Expr,
				Ascending:      s.Ascending,
				NullsFirst:     s.NullsFirst,
				NullsSpecified: s.NullsSpecified,
			}
		}
		return &operators.Sort{Child: child, SortSpecs: specs}, nil

	case *physical.Limit:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		return &operators.Limit{Child: child, Count: n.Count, Offset: n.Offset}, nil

	case *physical.SetOp:
		left, err := buildOperator(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := buildOperator(n.Right)
		if err != nil {
			return nil, err
		}
		return &operators.SetOpOp{
			Op:    n.Op,
			All:   n.All,
			Left:  left,
			Right: right,
		}, nil

	case *physical.Insert:
		ins := &operators.InsertOp{
			TableName: n.TableName,
			Table:     n.Table,
			Columns:   n.Columns,
			ValueRows: n.ValueRows,
		}
		if n.SelectSrc != nil {
			src, err := buildOperator(n.SelectSrc)
			if err != nil {
				return nil, err
			}
			ins.SelectSrc = src
		}
		return ins, nil

	case *physical.Update:
		assigns := make([]operators.UpdateAssign, len(n.Assigns))
		for i, a := range n.Assigns {
			assigns[i] = operators.UpdateAssign{Column: a.Column, Value: a.Value}
		}
		return &operators.UpdateOp{
			TableName: n.TableName,
			Table:     n.Table,
			Assigns:   assigns,
			Where:     n.Where,
		}, nil

	case *physical.Delete:
		return &operators.DeleteOp{
			TableName: n.TableName,
			Table:     n.Table,
			Where:     n.Where,
		}, nil

	case *physical.Explain:
		inner, err := buildOperator(n.Inner)
		if err != nil {
			return nil, err
		}
		return &operators.ExplainOp{Inner: inner, Analyze: n.Analyze, Plan: n.Inner}, nil

	case *physical.Window:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		return &operators.WindowOp{Child: child, Windows: n.Windows}, nil

	case *physical.CreateTable:
		ctOp := &operators.CreateTableOp{TableName: n.TableName, Columns: n.Columns}
		if n.SelectSrc != nil {
			src, err := buildOperator(n.SelectSrc)
			if err != nil {
				return nil, err
			}
			ctOp.SelectSrc = src
		}
		return ctOp, nil

	case *physical.DropTable:
		return &operators.DropTableOp{TableName: n.TableName, IfExists: n.IfExists}, nil

	case *physical.AlterTable:
		return &operators.AlterTableOp{
			TableName:  n.TableName,
			Action:     n.Action,
			ColDef:     n.ColDef,
			ColName:    n.ColName,
			NewName:    n.NewName,
			DefaultVal: n.DefaultVal,
		}, nil

	case *physical.Empty:
		return &operators.EmptyOp{Cols: n.Cols}, nil

	case *physical.ConstantScan:
		return &operators.ConstantScanOp{}, nil

	case *physical.Distinct:
		child, err := buildOperator(n.Child)
		if err != nil {
			return nil, err
		}
		return &operators.DedupeOp{Child: child}, nil

	default:
		return nil, fmt.Errorf("unsupported physical plan node: %T", plan)
	}
}
