package api

import (
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/stats"
	"github.com/query-engine/query-engine/internal/storage"
)

const (
	maxRequestBodyBytes = 1 << 20 // 1 MB
	maxSQLLength        = 64_000  // characters
	queryTimeoutSec     = 30      // seconds before a query is cancelled
)

// Server holds all dependencies for the HTTP API.
type Server struct {
	cat         *catalog.Catalog
	store       *storage.Storage
	statsMu     sync.RWMutex
	statsMap    map[string]*stats.TableStats
	corsOrigin  string
	seedToken   string // if non-empty, /api/schema/seed requires "Authorization: Bearer <token>"
}

// NewServer creates a new API server.
func NewServer(cat *catalog.Catalog, store *storage.Storage, statsMap map[string]*stats.TableStats, corsOrigin string) *Server {
	return &Server{
		cat:        cat,
		store:      store,
		statsMap:   statsMap,
		corsOrigin: corsOrigin,
	}
}

// WithSeedToken configures an API token required to call /api/schema/seed.
func (s *Server) WithSeedToken(token string) *Server {
	s.seedToken = token
	return s
}

// RefreshStats re-collects stats for all tables. Safe for concurrent use.
func (s *Server) RefreshStats() {
	fresh := make(map[string]*stats.TableStats)
	for _, name := range s.cat.List() {
		tbl, ok := s.cat.Lookup(name)
		if !ok {
			continue
		}
		ht, err := s.store.GetTable(name)
		if err != nil {
			continue
		}
		fresh[name] = stats.Collect(ht, tbl)
	}
	s.statsMu.Lock()
	s.statsMap = fresh
	s.statsMu.Unlock()
}

// getStatsMap returns a consistent snapshot of the stats map.
func (s *Server) getStatsMap() map[string]*stats.TableStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return s.statsMap
}

// Handler returns the chi router with all routes registered.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(s.corsOrigin))
	r.Use(maxBodyMiddleware(maxRequestBodyBytes))

	r.Get("/health", s.handleHealth)
	r.Post("/api/query", s.handleQuery)
	r.Post("/api/explain", s.handleExplain)
	r.Get("/api/schema", s.handleSchema)
	r.Get("/api/stats", s.handleStats)
	r.Post("/api/schema/table", s.handleCreateTable)
	r.Post("/api/schema/seed", s.handleSeed)

	return r
}
