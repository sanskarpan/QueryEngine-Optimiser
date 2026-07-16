package catalog

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Catalog is an in-memory schema registry. Thread-safe for reads after initialization.
type Catalog struct {
	mu     sync.RWMutex
	tables map[string]*Table
}

// New creates an empty catalog.
func New() *Catalog {
	return &Catalog{tables: make(map[string]*Table)}
}

// Register adds a table to the catalog. Returns an error on duplicate name.
func (c *Catalog) Register(table *Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(table.Name)
	if _, exists := c.tables[key]; exists {
		return fmt.Errorf("catalog: table %q already registered", table.Name)
	}
	c.tables[key] = table
	return nil
}

// MustRegister adds a table to the catalog. Panics on duplicate name.
// Follows Go convention where Must* functions panic on error.
func (c *Catalog) MustRegister(table *Table) {
	if err := c.Register(table); err != nil {
		panic(err.Error())
	}
}

// Lookup returns the table by name (case-insensitive).
func (c *Catalog) Lookup(name string) (*Table, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.tables[strings.ToLower(name)]
	return t, ok
}

// List returns all table names in sorted order.
func (c *Catalog) List() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.tables))
	for _, t := range c.tables {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

// Drop removes a table from the catalog. Returns false if it didn't exist.
func (c *Catalog) Drop(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(name)
	if _, ok := c.tables[key]; !ok {
		return false
	}
	delete(c.tables, key)
	return true
}

// AddColumn appends a column to an existing table's schema.
func (c *Catalog) AddColumn(tableName string, col Column) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(tableName)
	t, ok := c.tables[key]
	if !ok {
		return fmt.Errorf("catalog: table %q not found", tableName)
	}
	for _, existing := range t.Columns {
		if strings.EqualFold(existing.Name, col.Name) {
			return fmt.Errorf("catalog: column %q already exists in table %q", col.Name, tableName)
		}
	}
	col.Index = len(t.Columns)
	t.Columns = append(t.Columns, col)
	return nil
}

// DropColumn removes a column from an existing table's schema.
func (c *Catalog) DropColumn(tableName, colName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(tableName)
	t, ok := c.tables[key]
	if !ok {
		return fmt.Errorf("catalog: table %q not found", tableName)
	}
	idx := -1
	for i, col := range t.Columns {
		if strings.EqualFold(col.Name, colName) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("catalog: column %q not found in table %q", colName, tableName)
	}
	t.Columns = append(t.Columns[:idx], t.Columns[idx+1:]...)
	for i := range t.Columns {
		t.Columns[i].Index = i
	}
	return nil
}

// RenameColumn renames a column within an existing table.
func (c *Catalog) RenameColumn(tableName, oldColName, newColName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(tableName)
	t, ok := c.tables[key]
	if !ok {
		return fmt.Errorf("catalog: table %q not found", tableName)
	}
	for i, col := range t.Columns {
		if strings.EqualFold(col.Name, oldColName) {
			t.Columns[i].Name = newColName
			return nil
		}
	}
	return fmt.Errorf("catalog: column %q not found in table %q", oldColName, tableName)
}

// RenameTable renames a table in the catalog.
func (c *Catalog) RenameTable(oldName, newName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	oldKey, newKey := strings.ToLower(oldName), strings.ToLower(newName)
	t, ok := c.tables[oldKey]
	if !ok {
		return fmt.Errorf("catalog: table %q not found", oldName)
	}
	if _, exists := c.tables[newKey]; exists {
		return fmt.Errorf("catalog: table %q already exists", newName)
	}
	t.Name = newName
	c.tables[newKey] = t
	delete(c.tables, oldKey)
	return nil
}
