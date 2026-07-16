package storage

import "github.com/query-engine/query-engine/internal/catalog"

// Tuple is a single row of values.
type Tuple struct {
	Values []catalog.Value
}

// Clone returns a deep copy of the tuple.
func (t Tuple) Clone() Tuple {
	vals := make([]catalog.Value, len(t.Values))
	copy(vals, t.Values)
	return Tuple{Values: vals}
}
