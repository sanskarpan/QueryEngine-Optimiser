package cost

import (
	"math"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/stats"
)

// Estimator uses table statistics to estimate cardinalities and costs.
type Estimator struct {
	stats map[string]*stats.TableStats
	model *Model
}

// NewEstimator creates a cardinality estimator backed by the given stats map.
// Keys in the map should be lowercase table names.
func NewEstimator(s map[string]*stats.TableStats) *Estimator {
	return &Estimator{stats: s, model: NewModel()}
}

// Model returns the underlying cost model.
func (e *Estimator) Model() *Model { return e.model }

// EstimateRows estimates the output row count for a logical plan node.
func (e *Estimator) EstimateRows(plan logical.Plan) int64 {
	switch n := plan.(type) {
	case *logical.LogicalScan:
		return e.scanRows(n)
	case *logical.LogicalFilter:
		child := e.EstimateRows(n.Child)
		sel := selectivity(n.Predicate, n.Child, e.stats)
		rows := int64(float64(child) * sel)
		if rows < 1 {
			rows = 1
		}
		return rows
	case *logical.LogicalProject:
		return e.EstimateRows(n.Child)
	case *logical.LogicalJoin:
		left := e.EstimateRows(n.Left)
		right := e.EstimateRows(n.Right)
		return e.joinRows(left, right, n.JoinType, n.Condition, n.Left, n.Right)
	case *logical.LogicalAggregate:
		child := e.EstimateRows(n.Child)
		return e.aggRows(child, n.GroupBy, n.Child)
	case *logical.LogicalSort:
		return e.EstimateRows(n.Child)
	case *logical.LogicalLimit:
		child := e.EstimateRows(n.Child)
		if n.Count != nil {
			if lit, ok := n.Count.(*ast.IntLiteral); ok && lit.Value < child {
				return lit.Value
			}
		}
		return child
	case *logical.EmptyRelation:
		return 0
	case *logical.LogicalConstant:
		return 1
	}
	return 1
}

// EstimateCost estimates total execution cost for a logical plan.
func (e *Estimator) EstimateCost(plan logical.Plan) float64 {
	switch n := plan.(type) {
	case *logical.LogicalScan:
		rows := e.scanRows(n)
		pages := int64(1)
		if ts, ok := e.stats[strings.ToLower(n.TableName)]; ok {
			pages = ts.PageCount
		}
		return e.model.SeqScan(rows, pages)
	case *logical.LogicalFilter:
		return e.EstimateCost(n.Child) + float64(e.EstimateRows(n))
	case *logical.LogicalProject:
		return e.EstimateCost(n.Child)
	case *logical.LogicalJoin:
		lc := e.EstimateCost(n.Left)
		rc := e.EstimateCost(n.Right)
		lr := e.EstimateRows(n.Left)
		rr := e.EstimateRows(n.Right)
		var jc float64
		if n.JoinType == logical.InnerJoin {
			jc = e.model.HashJoin(lr, rr)
		} else {
			jc = e.model.NLJoin(lr, rr)
		}
		return lc + rc + jc
	case *logical.LogicalAggregate:
		cr := e.EstimateRows(n.Child)
		return e.EstimateCost(n.Child) + e.model.HashAgg(cr)
	case *logical.LogicalSort:
		cr := e.EstimateRows(n.Child)
		return e.EstimateCost(n.Child) + e.model.Sort(cr)
	case *logical.LogicalLimit:
		return e.EstimateCost(n.Child)
	}
	return 0
}

func (e *Estimator) scanRows(n *logical.LogicalScan) int64 {
	if ts, ok := e.stats[strings.ToLower(n.TableName)]; ok {
		return ts.RowCount
	}
	return 1000 // default if no stats
}

func (e *Estimator) joinRows(leftRows, rightRows int64, jt logical.JoinType, cond ast.Expression, left, right logical.Plan) int64 {
	switch jt {
	case logical.CrossJoin:
		return leftRows * rightRows
	case logical.LeftJoin:
		inner := innerJoinRows(leftRows, rightRows, cond, left, right, e.stats)
		if inner > leftRows {
			return inner
		}
		return leftRows
	case logical.RightJoin:
		inner := innerJoinRows(leftRows, rightRows, cond, left, right, e.stats)
		if inner > rightRows {
			return inner
		}
		return rightRows
	}
	return innerJoinRows(leftRows, rightRows, cond, left, right, e.stats)
}

func (e *Estimator) aggRows(childRows int64, groupBy []ast.Expression, child logical.Plan) int64 {
	if len(groupBy) == 0 {
		return 1
	}
	ndvProduct := int64(1)
	for _, expr := range groupBy {
		colRef, ok := expr.(*ast.ColumnRef)
		if !ok {
			ndvProduct *= 10
		} else {
			ndvProduct *= columnNDV(colRef, child, e.stats)
		}
		if ndvProduct >= childRows {
			return childRows
		}
	}
	if ndvProduct < 1 {
		return 1
	}
	return ndvProduct
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// selectivity estimates the fraction of rows passing a predicate (0.0–1.0).
func selectivity(pred ast.Expression, child logical.Plan, statsMap map[string]*stats.TableStats) float64 {
	if pred == nil {
		return 1.0
	}
	switch p := pred.(type) {
	case *ast.BinaryExpr:
		switch p.Op.Type {
		case lexer.AND:
			return selectivity(p.Left, child, statsMap) * selectivity(p.Right, child, statsMap)
		case lexer.OR:
			l := selectivity(p.Left, child, statsMap)
			r := selectivity(p.Right, child, statsMap)
			return l + r - l*r
		case lexer.EQ:
			if colRef, ok := p.Left.(*ast.ColumnRef); ok {
				ndv := columnNDV(colRef, child, statsMap)
				if ndv > 0 {
					return 1.0 / float64(ndv)
				}
			}
			return 0.1
		case lexer.GT, lexer.GTE, lexer.LT, lexer.LTE:
			return 0.3
		case lexer.NEQ:
			if colRef, ok := p.Left.(*ast.ColumnRef); ok {
				ndv := columnNDV(colRef, child, statsMap)
				if ndv > 0 {
					return 1.0 - 1.0/float64(ndv)
				}
			}
			return 0.9
		}
	case *ast.BoolLiteral:
		if p.Value {
			return 1.0
		}
		return 0.0
	case *ast.InExpr:
		return math.Min(float64(len(p.List))*0.1, 1.0)
	case *ast.IsNullExpr:
		return 0.05
	}
	return 0.3
}

// columnNDV returns the estimated number of distinct values for a column.
func columnNDV(colRef *ast.ColumnRef, child logical.Plan, statsMap map[string]*stats.TableStats) int64 {
	tbl, col := colRefParts(colRef)
	if tbl != "" {
		if ts, ok := statsMap[strings.ToLower(tbl)]; ok {
			if cs, ok := ts.Columns[col]; ok {
				return cs.DistinctCount
			}
		}
	}
	// Walk schema to find table alias → actual table name mapping.
	if child != nil {
		for _, c := range child.Schema() {
			parts := strings.SplitN(c.Name, ".", 2)
			if len(parts) == 2 && strings.EqualFold(parts[1], col) {
				if tbl == "" || strings.EqualFold(parts[0], tbl) {
					if ts, ok := statsMap[strings.ToLower(parts[0])]; ok {
						if cs, ok := ts.Columns[parts[1]]; ok {
							return cs.DistinctCount
						}
					}
				}
			}
		}
	}
	return 10 // default NDV
}

// innerJoinRows estimates inner join output cardinality.
func innerJoinRows(leftRows, rightRows int64, cond ast.Expression, left, right logical.Plan, statsMap map[string]*stats.TableStats) int64 {
	if cond == nil {
		return leftRows * rightRows
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op.Type != lexer.EQ {
		sel := selectivity(cond, nil, statsMap)
		result := int64(float64(leftRows*rightRows) * sel)
		if result < 1 {
			return 1
		}
		return result
	}
	// Equijoin: left.rows * right.rows / max(NDV_left, NDV_right)
	ndvL := int64(10)
	ndvR := int64(10)
	if cr, ok := bin.Left.(*ast.ColumnRef); ok {
		ndvL = columnNDV(cr, left, statsMap)
	}
	if cr, ok := bin.Right.(*ast.ColumnRef); ok {
		ndvR = columnNDV(cr, right, statsMap)
	}
	maxNDV := ndvL
	if ndvR > maxNDV {
		maxNDV = ndvR
	}
	if maxNDV == 0 {
		maxNDV = 1
	}
	result := leftRows * rightRows / maxNDV
	if result < 1 {
		return 1
	}
	return result
}

// colRefParts splits a ColumnRef into (table, column).
func colRefParts(colRef *ast.ColumnRef) (table, col string) {
	if colRef.Table != "" {
		return colRef.Table, colRef.Column
	}
	if colRef.ResolvedTable != "" {
		return colRef.ResolvedTable, colRef.Column
	}
	return "", colRef.Column
}
