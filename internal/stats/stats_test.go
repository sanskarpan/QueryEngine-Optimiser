package stats

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeHeapTable(rows [][]catalog.Value) *storage.HeapTable {
	ht := &storage.HeapTable{}
	for _, row := range rows {
		ht.Insert(storage.Tuple{Values: row})
	}
	return ht
}

func TestCollect_RowCount(t *testing.T) {
	ht := makeHeapTable([][]catalog.Value{
		{catalog.IntValue(1), catalog.TextValue("US")},
		{catalog.IntValue(2), catalog.TextValue("UK")},
		{catalog.IntValue(3), catalog.TextValue("US")},
	})
	table := &catalog.Table{
		Name: "test",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "country", Type: catalog.TypeText, Index: 1},
		},
	}
	ts := Collect(ht, table)
	assert.Equal(t, int64(3), ts.RowCount)
}

func TestCollect_DistinctCount(t *testing.T) {
	ht := makeHeapTable([][]catalog.Value{
		{catalog.IntValue(1), catalog.TextValue("US")},
		{catalog.IntValue(2), catalog.TextValue("UK")},
		{catalog.IntValue(3), catalog.TextValue("US")}, // US repeated
	})
	table := &catalog.Table{
		Name: "test",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "country", Type: catalog.TypeText, Index: 1},
		},
	}
	ts := Collect(ht, table)
	require.Contains(t, ts.Columns, "id")
	require.Contains(t, ts.Columns, "country")
	assert.Equal(t, int64(3), ts.Columns["id"].DistinctCount)
	assert.Equal(t, int64(2), ts.Columns["country"].DistinctCount)
}

func TestCollect_NullCount(t *testing.T) {
	ht := makeHeapTable([][]catalog.Value{
		{catalog.IntValue(1), catalog.NullValue()},
		{catalog.IntValue(2), catalog.TextValue("US")},
		{catalog.IntValue(3), catalog.NullValue()},
	})
	table := &catalog.Table{
		Name: "test",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "country", Type: catalog.TypeText, Index: 1},
		},
	}
	ts := Collect(ht, table)
	assert.Equal(t, int64(0), ts.Columns["id"].NullCount)
	assert.Equal(t, int64(2), ts.Columns["country"].NullCount)
}

func TestCollect_MinMax(t *testing.T) {
	ht := makeHeapTable([][]catalog.Value{
		{catalog.IntValue(5)},
		{catalog.IntValue(1)},
		{catalog.IntValue(3)},
	})
	table := &catalog.Table{
		Name:    "test",
		Columns: []catalog.Column{{Name: "val", Type: catalog.TypeInt, Index: 0}},
	}
	ts := Collect(ht, table)
	cs := ts.Columns["val"]
	require.NotNil(t, cs.MinValue)
	require.NotNil(t, cs.MaxValue)
	minV := cs.MinValue.(catalog.Value)
	maxV := cs.MaxValue.(catalog.Value)
	assert.Equal(t, int64(1), minV.IntVal)
	assert.Equal(t, int64(5), maxV.IntVal)
}

func TestCollect_Histogram(t *testing.T) {
	rows := make([][]catalog.Value, 100)
	for i := range rows {
		rows[i] = []catalog.Value{catalog.IntValue(int64(i + 1))}
	}
	ht := makeHeapTable(rows)
	table := &catalog.Table{
		Name:    "test",
		Columns: []catalog.Column{{Name: "val", Type: catalog.TypeInt, Index: 0}},
	}
	ts := Collect(ht, table)
	cs := ts.Columns["val"]
	// Should have at most numBuckets buckets.
	assert.LessOrEqual(t, len(cs.Histogram), numBuckets)
	assert.Greater(t, len(cs.Histogram), 0)
	// Frequencies should sum to 100.
	total := int64(0)
	for _, b := range cs.Histogram {
		total += b.Frequency
	}
	assert.Equal(t, int64(100), total)
}

func TestCollect_PageCount(t *testing.T) {
	// 250 rows → 2 full pages + 1 partial = 3 pages.
	rows := make([][]catalog.Value, 250)
	for i := range rows {
		rows[i] = []catalog.Value{catalog.IntValue(int64(i))}
	}
	ht := makeHeapTable(rows)
	table := &catalog.Table{
		Name:    "t",
		Columns: []catalog.Column{{Name: "id", Index: 0}},
	}
	ts := Collect(ht, table)
	assert.Equal(t, int64(3), ts.PageCount)
}
