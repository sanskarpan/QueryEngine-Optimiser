package catalog

import "strings"

// Column represents a single column in a table schema.
type Column struct {
	Name     string
	Type     DataType
	Nullable bool
	PK       bool
	Index    int // ordinal position (0-based)
}

// Table represents a table in the catalog.
type Table struct {
	Name    string
	Columns []Column
}

// FindColumn returns the column with the given name (case-insensitive), or nil.
func (t *Table) FindColumn(name string) *Column {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			return &t.Columns[i]
		}
	}
	return nil
}
