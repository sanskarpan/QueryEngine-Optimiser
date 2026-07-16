// Package exectypes holds shared types used by both executor and executor/operators
// to avoid circular imports.
package exectypes

import (
	"context"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
)

// SubqueryRunner executes a SELECT subquery. It is implemented in the executor
// package and injected into ExecContext before execution begins, allowing the
// expression evaluator to support EXISTS / IN (subquery) / scalar subquery.
type SubqueryRunner interface {
	RunSelect(sel *ast.SelectStatement, outerTuple *Tuple) ([]Tuple, error)
}

// ExecContext holds runtime resources available to all operators.
type ExecContext struct {
	Ctx            context.Context  // used to honour request cancellation / timeouts
	Storage        *storage.Storage
	Catalog        *catalog.Catalog
	Runner         SubqueryRunner // for subquery expression evaluation
	OuterTuple     *Tuple         // set by subquery runner for correlated subqueries
	SubqueryDepth  int            // current nesting depth; enforced to prevent stack exhaustion
	MemoryLimit    int64          // bytes (0 = unlimited)
	CTEs           map[string]*ast.SelectStatement // active CTE definitions (name → query)

	// Execution statistics
	RowsScanned  int64
	HashJoins    int
	SortOps      int
	RowsProduced int64
}

// NewExecContext creates a default execution context.
func NewExecContext(cat *catalog.Catalog, store *storage.Storage) *ExecContext {
	return &ExecContext{
		Catalog:     cat,
		Storage:     store,
		MemoryLimit: 256 * 1024 * 1024,
	}
}

// Tuple is a row of values with its associated schema.
type Tuple struct {
	Values []catalog.Value
	Schema []catalog.Column
}
