package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/pairing"
	"github.com/bertpratya/tervi/internal/server/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New builds the server's HTTP routes.
func New(cfg Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	pairing.Register(mux, pairing.Deps{Pool: pool, PublicURL: cfg.PublicURL, Logger: logger})
	web.Register(mux)

	return requireKnownHost(cfg, mux)
}

// Run opens the database, applies migrations, and serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	pairing.StartCleanup(ctx, pool, logger)

	listener, err := net.Listen("tcp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("listen on server address: %w", err)
	}
	server := &http.Server{Addr: cfg.ServerAddr, Handler: New(cfg, pool, logger)}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	logger.Info("server listening", "address", cfg.ServerAddr)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
