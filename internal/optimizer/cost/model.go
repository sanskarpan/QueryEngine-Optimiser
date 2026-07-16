// Package cost provides cost estimation and join ordering for CBO.
package cost

import "math"

const pageCost = 1.0

// Model computes estimated operator costs.
type Model struct{}

// NewModel creates a new cost model.
func NewModel() *Model { return &Model{} }

// SeqScan cost: pageCost per page.
func (m *Model) SeqScan(_, pages int64) float64 {
	return pageCost * float64(pages)
}

// HashJoin cost: 1.5 * build(inner) + 1.0 * probe(outer).
func (m *Model) HashJoin(outerRows, innerRows int64) float64 {
	return 1.5*float64(innerRows) + float64(outerRows)
}

// NLJoin cost: outer * inner * 0.01.
func (m *Model) NLJoin(outerRows, innerRows int64) float64 {
	return float64(outerRows) * float64(innerRows) * 0.01
}

// SortMergeJoin cost: sort both sides + merge.
func (m *Model) SortMergeJoin(outerRows, innerRows int64) float64 {
	return m.Sort(outerRows) + m.Sort(innerRows) + float64(outerRows+innerRows)
}

// HashAgg cost: 1.2 * child rows.
func (m *Model) HashAgg(rows int64) float64 {
	return 1.2 * float64(rows)
}

// Sort cost: n * log2(n) * 0.1.
func (m *Model) Sort(rows int64) float64 {
	if rows <= 1 {
		return 0
	}
	return float64(rows) * math.Log2(float64(rows)) * 0.1
}
