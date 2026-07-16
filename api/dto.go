// Package api contains HTTP handlers and DTOs for the query engine REST API.
package api

// --------------------------------------------------------------------------
// Request types
// --------------------------------------------------------------------------

// QueryRequest is the body for POST /api/query.
type QueryRequest struct {
	SQL     string         `json:"sql"`
	Options *QueryOptions  `json:"options,omitempty"`
}

// QueryOptions controls optional query features.
type QueryOptions struct {
	Explain      bool `json:"explain"`
	IncludeStats bool `json:"includeStats"`
}

// ExplainRequest is the body for POST /api/explain.
type ExplainRequest struct {
	SQL   string `json:"sql"`
	Stage string `json:"stage"` // "logical", "optimized", "physical"
}

// CreateTableRequest is the body for POST /api/schema/table.
type CreateTableRequest struct {
	SQL string `json:"sql"`
}

// --------------------------------------------------------------------------
// Response types
// --------------------------------------------------------------------------

// QueryResponse is returned by POST /api/query.
type QueryResponse struct {
	Columns          []string              `json:"columns"`
	Rows             [][]interface{}       `json:"rows"`
	RowCount         int                   `json:"rowCount"`
	ExecutionTimeMs  int64                 `json:"executionTimeMs"`
	Plan             *PlanBundle           `json:"plan,omitempty"`
	OptimizationSteps []OptimizationStep   `json:"optimizationSteps,omitempty"`
	Stats            *ExecStats            `json:"stats,omitempty"`
	Error            string                `json:"error,omitempty"`
}

// ExplainResponse is returned by POST /api/explain.
type ExplainResponse struct {
	Plan  *PlanBundle `json:"plan"`
	Error string      `json:"error,omitempty"`
}

// PlanBundle holds all three plan representations.
type PlanBundle struct {
	Logical   map[string]interface{} `json:"logical"`
	Optimized map[string]interface{} `json:"optimized"`
	Physical  map[string]interface{} `json:"physical"`
}

// OptimizationStep records one rule application in the RBO.
type OptimizationStep struct {
	Rule        string `json:"rule"`
	Applied     bool   `json:"applied"`
	Description string `json:"description"`
}

// ExecStats records execution counters.
type ExecStats struct {
	RowsScanned    int64 `json:"rowsScanned"`
	HashJoins      int64 `json:"hashJoins"`
	SortOperations int64 `json:"sortOperations"`
	RowsProduced   int64 `json:"rowsProduced"`
}

// SchemaResponse is returned by GET /api/schema.
type SchemaResponse struct {
	Tables []TableInfo `json:"tables"`
}

// TableInfo describes a single table.
type TableInfo struct {
	Name     string       `json:"name"`
	Columns  []ColumnInfo `json:"columns"`
	RowCount int64        `json:"rowCount"`
}

// ColumnInfo describes a single column.
type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
}

// StatsResponse is returned by GET /api/stats.
type StatsResponse struct {
	Tables map[string]*TableStatsDTO `json:"tables"`
}

// TableStatsDTO is the JSON form of stats.TableStats.
type TableStatsDTO struct {
	RowCount  int64                      `json:"rowCount"`
	PageCount int64                      `json:"pageCount"`
	Columns   map[string]*ColumnStatsDTO `json:"columns"`
}

// ColumnStatsDTO is the JSON form of stats.ColumnStats.
type ColumnStatsDTO struct {
	DistinctCount int64       `json:"distinctCount"`
	NullCount     int64       `json:"nullCount"`
	MinValue      interface{} `json:"minValue"`
	MaxValue      interface{} `json:"maxValue"`
	Histogram     []BucketDTO `json:"histogram,omitempty"`
}

// BucketDTO is the JSON form of stats.Bucket.
type BucketDTO struct {
	Low       interface{} `json:"low"`
	High      interface{} `json:"high"`
	Frequency int64       `json:"frequency"`
}

// ErrorResponse wraps an error message.
type ErrorResponse struct {
	Error   string `json:"error"`
	Stage   string `json:"stage,omitempty"`
	Line    int    `json:"line,omitempty"`
	Col     int    `json:"col,omitempty"`
}
