// Package physical defines the physical query plan nodes and the builder that converts
// an optimized logical plan into a concrete execution plan. Physical nodes map
// one-to-one with executor operators (SeqScan, HashJoin, Sort, Window, etc.).
package physical

import "github.com/query-engine/query-engine/internal/catalog"

// Plan is the interface all physical plan nodes implement.
type Plan interface {
	Children() []Plan
	Schema() []catalog.Column
	String() string
	ToJSON() map[string]interface{}
}
