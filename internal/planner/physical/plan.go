package physical

import "github.com/query-engine/query-engine/internal/catalog"

// Plan is the interface all physical plan nodes implement.
type Plan interface {
	Children() []Plan
	Schema() []catalog.Column
	String() string
	ToJSON() map[string]interface{}
}
