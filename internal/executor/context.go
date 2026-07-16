// Package executor orchestrates query execution using the Volcano model.
// Shared types (ExecContext, Tuple) live in the exectypes package to avoid import cycles.
package executor

import (
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/storage"
)

// Re-export types for convenience.
type ExecContext = exectypes.ExecContext
type Tuple = exectypes.Tuple

// NewExecContext creates a default execution context.
func NewExecContext(cat *catalog.Catalog, store *storage.Storage) *ExecContext {
	return exectypes.NewExecContext(cat, store)
}
