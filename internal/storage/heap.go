package storage

import (
	"sync"

	"github.com/query-engine/query-engine/internal/catalog"
)

// HeapTable is an in-memory row store protected by a read-write mutex.
type HeapTable struct {
	mu   sync.RWMutex
	rows []Tuple
}

// Insert appends a tuple to the table.
func (h *HeapTable) Insert(t Tuple) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append(h.rows, t)
}

// Scan returns a deep copy of all rows (safe for concurrent iteration and mutation).
func (h *HeapTable) Scan() []Tuple {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Tuple, len(h.rows))
	for i, row := range h.rows {
		result[i] = row.Clone()
	}
	return result
}

// RowCount returns the number of rows.
func (h *HeapTable) RowCount() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return int64(len(h.rows))
}

// Truncate removes all rows and releases the backing array.
func (h *HeapTable) Truncate() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = nil
}

// UpdateWhere updates rows that match pred by applying updater to each matched tuple.
// Returns the number of rows updated.
func (h *HeapTable) UpdateWhere(pred func(Tuple) bool, updater func(Tuple) Tuple) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var count int64
	for i, row := range h.rows {
		if pred(row) {
			h.rows[i] = updater(row)
			count++
		}
	}
	return count
}

// DeleteWhere removes rows that match pred.
// Returns the number of rows deleted.
func (h *HeapTable) DeleteWhere(pred func(Tuple) bool) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.rows[:0]
	var count int64
	for _, row := range h.rows {
		if pred(row) {
			count++
		} else {
			kept = append(kept, row)
		}
	}
	h.rows = kept
	return count
}

// AddColumnNulls appends a NULL value to every existing row (used by ALTER TABLE ADD COLUMN).
func (h *HeapTable) AddColumnNulls() {
	h.AddColumnDefault(catalog.NullValue())
}

// AddColumnDefault appends the given default value to every existing row.
func (h *HeapTable) AddColumnDefault(def catalog.Value) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.rows {
		h.rows[i].Values = append(h.rows[i].Values, def)
	}
}

// DropColumnValues removes the value at position idx from every existing row,
// keeping heap row data aligned with the schema after a DROP COLUMN.
func (h *HeapTable) DropColumnValues(idx int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.rows {
		vals := h.rows[i].Values
		if idx < len(vals) {
			newVals := make([]catalog.Value, 0, len(vals)-1)
			newVals = append(newVals, vals[:idx]...)
			newVals = append(newVals, vals[idx+1:]...)
			h.rows[i].Values = newVals
		}
	}
}
