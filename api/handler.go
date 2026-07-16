package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/executor"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/optimizer/rule"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
)

// --------------------------------------------------------------------------
// GET /health
// --------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	info, ok := debug.ReadBuildInfo()
	version := "unknown"
	if ok {
		version = info.Main.Version
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version,
	})
}

// --------------------------------------------------------------------------
// POST /api/query
// --------------------------------------------------------------------------

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "", 0, 0)
		return
	}
	if req.SQL == "" {
		writeError(w, http.StatusBadRequest, "sql field is required", "", 0, 0)
		return
	}
	if len(req.SQL) > maxSQLLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("sql too long: %d chars (max %d)", len(req.SQL), maxSQLLength), "", 0, 0)
		return
	}

	// Enforce a per-query execution timeout.
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeoutSec*time.Second)
	defer cancel()

	start := time.Now()

	// Parse
	p := parser.New(req.SQL)
	stmt, err := p.ParseStatement()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "parser", 0, 0)
		return
	}
	switch stmt.(type) {
	case *ast.SelectStatement, *ast.SetOpStatement, *ast.InsertStatement:
		// supported
	default:
		writeError(w, http.StatusBadRequest, "only SELECT and INSERT statements are supported", "parser", 0, 0)
		return
	}

	// Check cancellation before heavy lifting.
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "request cancelled", "", 0, 0)
		return
	}

	// Analyze
	a := analyzer.New(s.cat)
	if err := a.Analyze(stmt); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error(), "analyzer", 0, 0)
		return
	}

	// Logical plan
	lb := logical.NewBuilder(s.cat)
	lplan, err := lb.BuildStatement(stmt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "planner", 0, 0)
		return
	}

	logicalJSON := lplan.ToJSON()

	// Optimize
	statsMap := s.getStatsMap()
	opt := optimizer.New()
	var steps []rule.OptimizationStep
	oplan := opt.OptimizeWithCBO(lplan, statsMap, &steps)
	optimizedJSON := oplan.ToJSON()

	// Physical plan
	pb := physical.NewBuilderWithStats(statsMap)
	pplan, err := pb.Build(oplan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "physical planner", 0, 0)
		return
	}
	physicalJSON := pplan.ToJSON()

	// Check cancellation before execution.
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "query timed out during planning", "", 0, 0)
		return
	}

	// Execute — wire the HTTP context so the executor can honour timeouts.
	execCtx := exectypes.NewExecContext(s.cat, s.store)
	execCtx.Ctx = ctx
	execCtx.CTEs = lb.GetCTEs()
	result, err := executor.Execute(pplan, execCtx)
	if err != nil {
		// Distinguish timeout from other errors.
		if ctx.Err() != nil {
			writeError(w, http.StatusRequestTimeout, "query execution timed out", "executor", 0, 0)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "executor", 0, 0)
		return
	}

	elapsed := time.Since(start).Milliseconds()

	resp := QueryResponse{
		Columns:         result.Columns,
		Rows:            valuesToInterface(result.Rows),
		RowCount:        len(result.Rows),
		ExecutionTimeMs: elapsed,
	}

	if req.Options != nil && req.Options.Explain {
		resp.Plan = &PlanBundle{
			Logical:   logicalJSON,
			Optimized: optimizedJSON,
			Physical:  physicalJSON,
		}
		resp.OptimizationSteps = convertSteps(steps)
	}

	if req.Options != nil && req.Options.IncludeStats {
		resp.Stats = &ExecStats{
			RowsScanned:    execCtx.RowsScanned,
			HashJoins:      int64(execCtx.HashJoins),
			SortOperations: int64(execCtx.SortOps),
			RowsProduced:   execCtx.RowsProduced,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --------------------------------------------------------------------------
// POST /api/explain
// --------------------------------------------------------------------------

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	var req ExplainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "", 0, 0)
		return
	}
	if len(req.SQL) > maxSQLLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("sql too long: %d chars (max %d)", len(req.SQL), maxSQLLength), "", 0, 0)
		return
	}

	p := parser.New(req.SQL)
	stmt, err := p.ParseStatement()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "parser", 0, 0)
		return
	}
	switch stmt.(type) {
	case *ast.SelectStatement, *ast.SetOpStatement:
		// supported for explain
	default:
		writeError(w, http.StatusBadRequest, "only SELECT statements are supported for EXPLAIN", "parser", 0, 0)
		return
	}

	a := analyzer.New(s.cat)
	if err := a.Analyze(stmt); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error(), "analyzer", 0, 0)
		return
	}

	lb := logical.NewBuilder(s.cat)
	lplan, err := lb.BuildStatement(stmt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "planner", 0, 0)
		return
	}

	statsMap := s.getStatsMap()
	opt := optimizer.New()
	var steps []rule.OptimizationStep
	oplan := opt.OptimizeWithCBO(lplan, statsMap, &steps)

	pb := physical.NewBuilderWithStats(statsMap)
	pplan, err := pb.Build(oplan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "physical planner", 0, 0)
		return
	}

	writeJSON(w, http.StatusOK, ExplainResponse{
		Plan: &PlanBundle{
			Logical:   lplan.ToJSON(),
			Optimized: oplan.ToJSON(),
			Physical:  pplan.ToJSON(),
		},
	})
}

// --------------------------------------------------------------------------
// GET /api/schema
// --------------------------------------------------------------------------

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	names := s.cat.List()
	tables := make([]TableInfo, 0, len(names))
	for _, name := range names {
		tbl, ok := s.cat.Lookup(name)
		if !ok {
			continue
		}
		rowCount := int64(0)
		if ht, err := s.store.GetTable(name); err == nil {
			rowCount = ht.RowCount()
		}
		cols := make([]ColumnInfo, len(tbl.Columns))
		for i, col := range tbl.Columns {
			cols[i] = ColumnInfo{
				Name:       col.Name,
				Type:       col.Type.String(),
				Nullable:   col.Nullable,
				PrimaryKey: col.PK,
			}
		}
		tables = append(tables, TableInfo{
			Name:     name,
			Columns:  cols,
			RowCount: rowCount,
		})
	}
	writeJSON(w, http.StatusOK, SchemaResponse{Tables: tables})
}

// --------------------------------------------------------------------------
// GET /api/stats
// --------------------------------------------------------------------------

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	statsMap := s.getStatsMap()
	out := make(map[string]*TableStatsDTO, len(statsMap))
	for name, ts := range statsMap {
		dto := &TableStatsDTO{
			RowCount:  ts.RowCount,
			PageCount: ts.PageCount,
			Columns:   make(map[string]*ColumnStatsDTO, len(ts.Columns)),
		}
		for colName, cs := range ts.Columns {
			bkts := make([]BucketDTO, len(cs.Histogram))
			for i, b := range cs.Histogram {
				bkts[i] = BucketDTO{
					Low:       valueString(b.Low),
					High:      valueString(b.High),
					Frequency: b.Frequency,
				}
			}
			dto.Columns[colName] = &ColumnStatsDTO{
				DistinctCount: cs.DistinctCount,
				NullCount:     cs.NullCount,
				MinValue:      valueString(cs.MinValue),
				MaxValue:      valueString(cs.MaxValue),
				Histogram:     bkts,
			}
		}
		out[name] = dto
	}
	writeJSON(w, http.StatusOK, StatsResponse{Tables: out})
}

// --------------------------------------------------------------------------
// POST /api/schema/table
// --------------------------------------------------------------------------

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	var req CreateTableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "", 0, 0)
		return
	}

	p := parser.New(req.SQL)
	stmt, err := p.ParseStatement()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "parser", 0, 0)
		return
	}

	ct, ok := stmt.(*ast.CreateTableStatement)
	if !ok {
		writeError(w, http.StatusBadRequest, "expected CREATE TABLE statement", "parser", 0, 0)
		return
	}

	if len(ct.Columns) == 0 {
		writeError(w, http.StatusBadRequest, "table must have at least one column", "parser", 0, 0)
		return
	}

	// Semantic analysis: duplicate columns, invalid types, etc.
	a := analyzer.New(s.cat)
	if err := a.Analyze(stmt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "analyzer", 0, 0)
		return
	}

	cols := make([]catalog.Column, len(ct.Columns))
	for i, cd := range ct.Columns {
		dt, err := catalog.ParseDataType(cd.TypeName)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("column %q: %v", cd.Name, err), "parser", 0, 0)
			return
		}
		cols[i] = catalog.Column{
			Name:     cd.Name,
			Type:     dt,
			Nullable: !cd.NotNull,
			PK:       cd.PrimaryKey,
			Index:    i,
		}
	}

	tbl := &catalog.Table{Name: ct.Name, Columns: cols}
	if err := s.cat.Register(tbl); err != nil {
		writeError(w, http.StatusConflict, err.Error(), "catalog", 0, 0)
		return
	}
	// Atomicity: if storage creation fails, remove the catalog entry to avoid inconsistency.
	if err := s.store.CreateTable(ct.Name); err != nil {
		s.cat.Drop(ct.Name)
		writeError(w, http.StatusInternalServerError, err.Error(), "storage", 0, 0)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"table": ct.Name})
}

// --------------------------------------------------------------------------
// POST /api/schema/seed
// --------------------------------------------------------------------------

func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	if s.seedToken != "" {
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + s.seedToken
		if auth != expected {
			writeError(w, http.StatusUnauthorized, "valid Authorization: Bearer <token> header required", "auth", 0, 0)
			return
		}
	}
	if err := storage.Reset(s.cat, s.store); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "seed", 0, 0)
		return
	}
	s.RefreshStats()
	writeJSON(w, http.StatusOK, map[string]string{"status": "seeded"})
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg, stage string, line, col int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{ //nolint:errcheck
		Error: msg,
		Stage: stage,
		Line:  line,
		Col:   col,
	})
}

func valuesToInterface(rows [][]catalog.Value) [][]interface{} {
	out := make([][]interface{}, len(rows))
	for i, row := range rows {
		r := make([]interface{}, len(row))
		for j, v := range row {
			if v.IsNull {
				r[j] = nil
			} else {
				switch v.Type {
				case catalog.TypeInt:
					r[j] = v.IntVal
				case catalog.TypeFloat:
					r[j] = v.FloatVal
				case catalog.TypeBool:
					r[j] = v.BoolVal
				default:
					r[j] = v.StrVal
				}
			}
		}
		out[i] = r
	}
	return out
}

func convertSteps(steps []rule.OptimizationStep) []OptimizationStep {
	out := make([]OptimizationStep, len(steps))
	for i, s := range steps {
		out[i] = OptimizationStep{
			Rule:        s.Rule,
			Applied:     s.Applied,
			Description: s.Description,
		}
	}
	return out
}

func valueString(v any) interface{} {
	if v == nil {
		return nil
	}
	if cv, ok := v.(catalog.Value); ok {
		return cv.String()
	}
	return fmt.Sprintf("%v", v)
}
