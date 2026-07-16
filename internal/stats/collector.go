package stats

import (
	"sort"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
)

const (
	rowsPerPage = 100
	numBuckets  = 10
)

// Collect builds TableStats by scanning a heap table.
func Collect(heapTable *storage.HeapTable, table *catalog.Table) *TableStats {
	rows := heapTable.Scan()
	rowCount := int64(len(rows))
	pageCount := rowCount/rowsPerPage + 1

	colStats := make(map[string]*ColumnStats, len(table.Columns))
	for _, col := range table.Columns {
		colStats[col.Name] = computeColStats(rows, col.Index)
	}

	return &TableStats{
		RowCount:  rowCount,
		PageCount: pageCount,
		Columns:   colStats,
	}
}

func computeColStats(rows []storage.Tuple, colIdx int) *ColumnStats {
	seen := make(map[string]struct{})
	var nullCount int64
	var values []catalog.Value

	for _, row := range rows {
		if colIdx >= len(row.Values) {
			nullCount++
			continue
		}
		v := row.Values[colIdx]
		if v.IsNull {
			nullCount++
			continue
		}
		seen[v.String()] = struct{}{}
		values = append(values, v)
	}

	cs := &ColumnStats{
		DistinctCount: int64(len(seen)),
		NullCount:     nullCount,
	}
	if len(values) == 0 {
		return cs
	}

	sort.Slice(values, func(i, j int) bool {
		cmp, err := values[i].Compare(values[j])
		return err == nil && cmp < 0
	})

	cs.MinValue = values[0]
	cs.MaxValue = values[len(values)-1]
	cs.Histogram = buildHistogram(values, numBuckets)
	return cs
}

// buildHistogram creates an equi-depth histogram with at most n buckets.
func buildHistogram(sorted []catalog.Value, n int) []Bucket {
	total := len(sorted)
	if total == 0 || n <= 0 {
		return nil
	}
	if n > total {
		n = total
	}
	bucketSize := (total + n - 1) / n
	buckets := make([]Bucket, 0, n)
	for start := 0; start < total; start += bucketSize {
		end := start + bucketSize
		if end > total {
			end = total
		}
		buckets = append(buckets, Bucket{
			Low:       sorted[start],
			High:      sorted[end-1],
			Frequency: int64(end - start),
		})
	}
	return buckets
}
