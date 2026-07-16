// Package storage provides an in-memory relational storage engine.
// Each table is a heap of typed tuples protected by a read-write mutex,
// supporting concurrent reads and serialized writes.
package storage

import (
	"fmt"
	"strings"
	"sync"
)

// Storage is a registry of HeapTables, keyed by table name.
type Storage struct {
	mu     sync.RWMutex
	tables map[string]*HeapTable
}

// New creates an empty storage registry.
func New() *Storage {
	return &Storage{tables: make(map[string]*HeapTable)}
}

// CreateTable creates a new empty HeapTable. Returns an error if it already exists.
func (s *Storage) CreateTable(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	if _, exists := s.tables[key]; exists {
		return fmt.Errorf("storage: table %q already exists", name)
	}
	s.tables[key] = &HeapTable{}
	return nil
}

// GetTable returns the HeapTable for the given name, or an error if not found.
func (s *Storage) GetTable(name string) (*HeapTable, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.tables[strings.ToLower(name)]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("storage: table %q not found", name)
}

// MustGetTable panics if the table is not found (use for tests / seed data).
func (s *Storage) MustGetTable(name string) *HeapTable {
	t, err := s.GetTable(name)
	if err != nil {
		panic(err)
	}
	return t
}

// DropTable removes a table from storage.
func (s *Storage) DropTable(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	if _, ok := s.tables[key]; !ok {
		return false
	}
	delete(s.tables, key)
	return true
}

// RenameTable moves a table's storage entry from oldName to newName.
func (s *Storage) RenameTable(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldKey, newKey := strings.ToLower(oldName), strings.ToLower(newName)
	ht, ok := s.tables[oldKey]
	if !ok {
		return fmt.Errorf("storage: table %q not found", oldName)
	}
	s.tables[newKey] = ht
	delete(s.tables, oldKey)
	return nil
}
