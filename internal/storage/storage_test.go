package storage

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeapTable_InsertAndScan(t *testing.T) {
	h := &HeapTable{}
	h.Insert(Tuple{Values: []catalog.Value{catalog.IntValue(1), catalog.TextValue("Alice")}})
	h.Insert(Tuple{Values: []catalog.Value{catalog.IntValue(2), catalog.TextValue("Bob")}})

	rows := h.Scan()
	assert.Len(t, rows, 2)
	assert.Equal(t, catalog.IntValue(1), rows[0].Values[0])
	assert.Equal(t, catalog.TextValue("Alice"), rows[0].Values[1])
}

func TestHeapTable_RowCount(t *testing.T) {
	h := &HeapTable{}
	assert.Equal(t, int64(0), h.RowCount())
	h.Insert(Tuple{Values: []catalog.Value{catalog.IntValue(1)}})
	assert.Equal(t, int64(1), h.RowCount())
}

func TestHeapTable_Scan_ReturnsCopy(t *testing.T) {
	h := &HeapTable{}
	h.Insert(Tuple{Values: []catalog.Value{catalog.IntValue(1)}})
	rows := h.Scan()
	// Modifying the returned slice should not affect the table
	rows[0].Values[0] = catalog.IntValue(999)
	assert.Equal(t, int64(1), h.RowCount())
	rows2 := h.Scan()
	assert.Equal(t, catalog.IntValue(1), rows2[0].Values[0])
}

func TestHeapTable_Truncate(t *testing.T) {
	h := &HeapTable{}
	h.Insert(Tuple{Values: []catalog.Value{catalog.IntValue(1)}})
	h.Truncate()
	assert.Equal(t, int64(0), h.RowCount())
}

func TestStorage_CreateAndGet(t *testing.T) {
	s := New()
	require.NoError(t, s.CreateTable("orders"))

	table, err := s.GetTable("orders")
	require.NoError(t, err)
	assert.NotNil(t, table)
}

func TestStorage_CreateDuplicate_Error(t *testing.T) {
	s := New()
	require.NoError(t, s.CreateTable("foo"))
	err := s.CreateTable("foo")
	assert.Error(t, err)
}

func TestStorage_GetNotFound_Error(t *testing.T) {
	s := New()
	_, err := s.GetTable("missing")
	assert.Error(t, err)
}

func TestStorage_CaseInsensitive(t *testing.T) {
	s := New()
	require.NoError(t, s.CreateTable("Orders"))
	_, err := s.GetTable("orders")
	assert.NoError(t, err)
	_, err = s.GetTable("ORDERS")
	assert.NoError(t, err)
}

func TestStorage_Drop(t *testing.T) {
	s := New()
	require.NoError(t, s.CreateTable("tmp"))
	assert.True(t, s.DropTable("tmp"))
	assert.False(t, s.DropTable("tmp"))
}

func TestTuple_Clone(t *testing.T) {
	original := Tuple{Values: []catalog.Value{catalog.IntValue(42), catalog.TextValue("hi")}}
	clone := original.Clone()
	clone.Values[0] = catalog.IntValue(999)
	// Original unchanged
	assert.Equal(t, catalog.IntValue(42), original.Values[0])
}
