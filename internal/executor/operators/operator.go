package operators

import (
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
)

// Operator is the Volcano iterator interface.
type Operator interface {
	// Open initializes the operator. Must be called before Next.
	Open(ctx *exectypes.ExecContext) error
	// Next returns the next tuple, or (nil, nil) at EOF, or (nil, err) on error.
	Next() (*exectypes.Tuple, error)
	// Close releases any resources.
	Close() error
	// Schema returns the output column schema.
	Schema() []catalog.Column
}
