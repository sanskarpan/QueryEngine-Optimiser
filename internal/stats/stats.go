// Package stats provides statistics types and collection for CBO.
package stats

// TableStats holds per-table statistics for cardinality estimation.
type TableStats struct {
	RowCount  int64
	PageCount int64
	Columns   map[string]*ColumnStats
}

// ColumnStats holds per-column statistics.
type ColumnStats struct {
	DistinctCount int64
	NullCount     int64
	MinValue      any // catalog.Value or nil
	MaxValue      any // catalog.Value or nil
	Histogram     []Bucket
}

// Bucket is one slice of an equi-depth histogram.
type Bucket struct {
	Low, High any // catalog.Value
	Frequency int64
}
