package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/stats"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServer creates a server with seeded data and collected stats.
func testServer(t *testing.T) *Server {
	t.Helper()
	cat := catalog.New()
	store := storage.New()
	require.NoError(t, storage.Seed(cat, store))

	statsMap := make(map[string]*stats.TableStats)
	for _, name := range cat.List() {
		tbl, ok := cat.Lookup(name)
		if !ok {
			continue
		}
		ht, err := store.GetTable(name)
		require.NoError(t, err)
		statsMap[name] = stats.Collect(ht, tbl)
	}
	return NewServer(cat, store, statsMap, "*")
}

func postJSON(t *testing.T, srv *Server, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func getPath(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// --------------------------------------------------------------------------
// POST /api/query
// --------------------------------------------------------------------------

func TestHandleQuery_SimpleScan(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT id FROM customers LIMIT 5"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 5, resp.RowCount)
	assert.Equal(t, []string{"id"}, resp.Columns)
}

func TestHandleQuery_CountStar(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT COUNT(*) FROM orders"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.RowCount)
	count := resp.Rows[0][0].(float64) // JSON numbers decode to float64
	assert.Equal(t, float64(1000), count)
}

func TestHandleQuery_WithExplain(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{
		SQL:     "SELECT id FROM customers LIMIT 3",
		Options: &QueryOptions{Explain: true},
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Plan)
	assert.NotEmpty(t, resp.Plan.Logical)
	assert.NotEmpty(t, resp.Plan.Optimized)
	assert.NotEmpty(t, resp.Plan.Physical)
}

func TestHandleQuery_WithStats(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{
		SQL:     "SELECT id FROM orders",
		Options: &QueryOptions{IncludeStats: true},
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Stats)
	assert.Greater(t, resp.Stats.RowsScanned, int64(0))
}

func TestHandleQuery_Join(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{
		SQL: "SELECT c.id, o.id FROM customers c JOIN orders o ON c.id = o.customer_id LIMIT 5",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 5, resp.RowCount)
}

func TestHandleQuery_GroupBy(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{
		SQL: "SELECT status, COUNT(*) FROM orders GROUP BY status",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Greater(t, resp.RowCount, 0)
	assert.LessOrEqual(t, resp.RowCount, 4)
}

func TestHandleQuery_ParseError(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT FROM"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleQuery_UnknownTable(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT id FROM nonexistent_table"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandleQuery_EmptySQL(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: ""})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --------------------------------------------------------------------------
// POST /api/explain
// --------------------------------------------------------------------------

func TestHandleExplain(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/explain", ExplainRequest{
		SQL: "SELECT id FROM customers WHERE id > 50",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExplainResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Plan)
	assert.NotEmpty(t, resp.Plan.Logical)
	assert.NotEmpty(t, resp.Plan.Physical)
}

// --------------------------------------------------------------------------
// GET /api/schema
// --------------------------------------------------------------------------

func TestHandleSchema(t *testing.T) {
	srv := testServer(t)
	w := getPath(t, srv, "/api/schema")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp SchemaResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.GreaterOrEqual(t, len(resp.Tables), 3) // customers, products, orders
}

// --------------------------------------------------------------------------
// GET /api/stats
// --------------------------------------------------------------------------

func TestHandleStats(t *testing.T) {
	srv := testServer(t)
	w := getPath(t, srv, "/api/stats")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp StatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.Tables, "orders")
	assert.Equal(t, int64(1000), resp.Tables["orders"].RowCount)
}

// --------------------------------------------------------------------------
// POST /api/schema/seed
// --------------------------------------------------------------------------

func TestHandleSeed(t *testing.T) {
	srv := testServer(t)
	// First query to confirm data exists.
	w1 := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT COUNT(*) FROM orders"})
	assert.Equal(t, http.StatusOK, w1.Code)

	// Seed resets data.
	w2 := postJSON(t, srv, "/api/schema/seed", nil)
	assert.Equal(t, http.StatusOK, w2.Code)

	// Data should be back.
	w3 := postJSON(t, srv, "/api/query", QueryRequest{SQL: "SELECT COUNT(*) FROM orders"})
	assert.Equal(t, http.StatusOK, w3.Code)
	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp))
	count := resp.Rows[0][0].(float64)
	assert.Equal(t, float64(1000), count)
}

// --------------------------------------------------------------------------
// CORS
// --------------------------------------------------------------------------

func TestCORSHeaders(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/query", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

// --------------------------------------------------------------------------
// GET /health
// --------------------------------------------------------------------------

func TestHandleHealth(t *testing.T) {
	srv := testServer(t)
	w := getPath(t, srv, "/health")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
}

// --------------------------------------------------------------------------
// Security / validation
// --------------------------------------------------------------------------

func TestHandleQuery_SQLTooLong(t *testing.T) {
	srv := testServer(t)
	sql := make([]byte, maxSQLLength+1)
	for i := range sql {
		sql[i] = 'x'
	}
	w := postJSON(t, srv, "/api/query", QueryRequest{SQL: string(sql)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleQuery_Insert(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/api/query", QueryRequest{
		SQL: "INSERT INTO products (id, name, category, price) VALUES (7777, 'Test', 'misc', 1.00)",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp QueryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.RowCount)
}
