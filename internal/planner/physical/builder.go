package physical

import (
	"fmt"
	"strings"

	"github.com/query-engine/query-engine/internal/optimizer/cost"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/stats"
)

// Builder converts an optimized logical plan into a physical execution plan.
type Builder struct {
	est *cost.Estimator // nil = always pick default algorithm
}

func NewBuilder() *Builder { return &Builder{} }

// NewBuilderWithStats creates a builder that uses cost estimation to select join algorithms.
func NewBuilderWithStats(statsMap map[string]*stats.TableStats) *Builder {
	return &Builder{est: cost.NewEstimator(statsMap)}
}

// Build converts a logical plan to a physical plan.
func (b *Builder) Build(plan logical.Plan) (Plan, error) {
	return b.build(plan)
}

func (b *Builder) build(plan logical.Plan) (Plan, error) {
	switch n := plan.(type) {
	case *logical.LogicalScan:
		return &SeqScan{TableName: n.TableName, Alias: n.Alias, Table: n.Table}, nil

	case *logical.LogicalFilter:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		return &Filter{Child: child, Predicate: n.Predicate}, nil

	case *logical.LogicalProject:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		return &Projection{Child: child, Expressions: n.Expressions, Aliases: n.Aliases}, nil

	case *logical.LogicalJoin:
		left, err := b.build(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := b.build(n.Right)
		if err != nil {
			return nil, err
		}
		jt := logicalToPhysicalJoin(n.JoinType)
		if n.JoinType == logical.InnerJoin {
			// Use cost model to pick best join algorithm when stats are available.
			if b.est != nil {
				leftRows := b.est.EstimateRows(n.Left)
				rightRows := b.est.EstimateRows(n.Right)
				m := b.est.Model()
				hashCost := m.HashJoin(leftRows, rightRows)
				nlCost := m.NLJoin(leftRows, rightRows)
				smjCost := m.SortMergeJoin(leftRows, rightRows)
				if smjCost < hashCost && smjCost < nlCost {
					return &SortMergeJoin{Left: left, Right: right, JoinType: jt, Condition: n.Condition}, nil
				}
				if hashCost <= nlCost {
					return &HashJoin{Left: left, Right: right, JoinType: jt, Condition: n.Condition}, nil
				}
				return &NestedLoopJoin{Left: left, Right: right, JoinType: jt, Condition: n.Condition}, nil
			}
			return &HashJoin{Left: left, Right: right, JoinType: jt, Condition: n.Condition}, nil
		}
		return &NestedLoopJoin{Left: left, Right: right, JoinType: jt, Condition: n.Condition}, nil

	case *logical.LogicalAggregate:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		aggs := make([]AggExpr, len(n.Aggs))
		for i, a := range n.Aggs {
			aggs[i] = AggExpr{Function: a.Function, Arg: a.Arg, StarArg: a.StarArg, Distinct: a.Distinct, Alias: a.Alias}
		}
		return &HashAggregate{Child: child, GroupBy: n.GroupBy, Aggs: aggs}, nil

	case *logical.LogicalSort:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		specs := make([]SortSpec, len(n.SortSpecs))
		for i, s := range n.SortSpecs {
			specs[i] = SortSpec{
				Expr:           s.Expr,
				Ascending:      s.Ascending,
				NullsFirst:     s.NullsFirst,
				NullsSpecified: s.NullsSpecified,
			}
		}
		return &Sort{Child: child, SortSpecs: specs}, nil

	case *logical.LogicalLimit:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		return &Limit{Child: child, Count: n.Count, Offset: n.Offset}, nil

	case *logical.LogicalSubquery:
		return b.build(n.Child)

	case *logical.LogicalSetOp:
		left, err := b.build(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := b.build(n.Right)
		if err != nil {
			return nil, err
		}
		return &SetOp{Op: n.Op, All: n.All, Left: left, Right: right}, nil

	case *logical.LogicalInsert:
		ins := &Insert{TableName: n.TableName, Table: n.Table, Columns: n.Columns, ValueRows: n.ValueRows}
		if n.SelectSrc != nil {
			src, err := b.build(n.SelectSrc)
			if err != nil {
				return nil, err
			}
			ins.SelectSrc = src
		}
		return ins, nil

	case *logical.LogicalUpdate:
		assigns := make([]UpdateAssign, len(n.Assigns))
		for i, a := range n.Assigns {
			assigns[i] = UpdateAssign{Column: a.Column, Value: a.Value}
		}
		return &Update{TableName: n.TableName, Table: n.Table, Assigns: assigns, Where: n.Where}, nil

	case *logical.LogicalDelete:
		return &Delete{TableName: n.TableName, Table: n.Table, Where: n.Where}, nil

	case *logical.LogicalExplain:
		inner, err := b.build(n.Inner)
		if err != nil {
			return nil, err
		}
		return &Explain{Inner: inner, Analyze: n.Analyze}, nil

	case *logical.LogicalWindow:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		wins := make([]WindowExpr, len(n.Windows))
		for i, w := range n.Windows {
			wins[i] = WindowExpr{Expr: w.Expr, Alias: w.Alias}
		}
		return &Window{Child: child, Windows: wins}, nil

	case *logical.LogicalCreateTable:
		ct := &CreateTable{TableName: n.TableName, Columns: n.Columns}
		if n.SelectSrc != nil {
			src, err := b.build(n.SelectSrc)
			if err != nil {
				return nil, err
			}
			ct.SelectSrc = src
		}
		return ct, nil

	case *logical.LogicalDropTable:
		return &DropTable{TableName: n.TableName, IfExists: n.IfExists}, nil

	case *logical.LogicalAlterTable:
		return &AlterTable{
			TableName:  n.TableName,
			Action:     n.Action,
			ColDef:     n.ColDef,
			ColName:    n.ColName,
			NewName:    n.NewName,
			DefaultVal: n.DefaultVal,
		}, nil

	case *logical.EmptyRelation:
		return &Empty{Cols: n.Cols}, nil

	case *logical.LogicalConstant:
		return &ConstantScan{}, nil

	case *logical.LogicalDistinct:
		child, err := b.build(n.Child)
		if err != nil {
			return nil, err
		}
		return &Distinct{Child: child}, nil

	default:
		return nil, fmt.Errorf("physical planner: unsupported logical node type: %T", plan)
	}
}

func logicalToPhysicalJoin(jt logical.JoinType) JoinType {
	switch jt {
	case logical.LeftJoin:
		return LeftJoin
	case logical.RightJoin:
		return RightJoin
	case logical.CrossJoin:
		return CrossJoin
	case logical.FullJoin:
		return FullJoin
	default:
		return InnerJoin
	}
}

// PrintPlan returns an indented tree string for a physical plan.
func PrintPlan(p Plan) string {
	var sb strings.Builder
	printPlanNode(&sb, p, 0)
	return strings.TrimRight(sb.String(), "\n")
}

func printPlanNode(sb *strings.Builder, p Plan, depth int) {
	sb.WriteString(fmt.Sprintf("%s%s\n", strings.Repeat("  ", depth), p.String()))
	for _, child := range p.Children() {
		printPlanNode(sb, child, depth+1)
	}
}
