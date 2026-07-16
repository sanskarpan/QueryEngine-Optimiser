package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/query-engine/query-engine/api"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/stats"
	"github.com/query-engine/query-engine/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "*"
	}

	cat := catalog.New()
	store := storage.New()

	if err := storage.Seed(cat, store); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}

	statsMap := collectStats(cat, store)
	srv := api.NewServer(cat, store, statsMap, corsOrigin)

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	} else {
		slog.Info("server stopped cleanly")
	}
}

func collectStats(cat *catalog.Catalog, store *storage.Storage) map[string]*stats.TableStats {
	m := make(map[string]*stats.TableStats)
	for _, name := range cat.List() {
		tbl, ok := cat.Lookup(name)
		if !ok {
			continue
		}
		ht, err := store.GetTable(name)
		if err != nil {
			continue
		}
		m[name] = stats.Collect(ht, tbl)
	}
	return m
}
