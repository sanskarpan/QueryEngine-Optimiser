package logical

import (
	"encoding/json"
	"sync/atomic"

	"github.com/query-engine/query-engine/internal/catalog"
)

// Plan is the interface all logical plan nodes implement.
type Plan interface {
	// Children returns the child plans (inputs to this operator).
	Children() []Plan
	// Schema returns the output column schema of this node.
	Schema() []catalog.Column
	// String returns an indented human-readable representation.
	String() string
	// ToJSON returns a JSON-serializable map for API responses.
	ToJSON() map[string]interface{}
}

// planIDCounter generates unique node IDs; atomic to avoid data races under concurrent requests.
var planIDCounter int64

func nextID() int64 {
	return atomic.AddInt64(&planIDCounter, 1)
}

func resetID() {
	atomic.StoreInt64(&planIDCounter, 0)
}

// marshalPlan converts a plan tree to JSON bytes.
func MarshalPlan(p Plan) ([]byte, error) {
	resetID()
	m := p.ToJSON()
	return json.MarshalIndent(m, "", "  ")
}
