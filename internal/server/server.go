package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bertpratya/tervi/internal/server/apidocs"
	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/machines"
	"github.com/bertpratya/tervi/internal/server/pairing"
	"github.com/bertpratya/tervi/internal/server/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New builds the server's HTTP routes.
func New(cfg Config, pool *pgxpool.Pool, logger *slog.Logger, connections *connection.List) http.Handler {
	if connections == nil {
		connections = connection.NewList()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	pairing.Register(mux, pairing.Deps{Pool: pool, PublicURL: cfg.PublicURL, Logger: logger})
	connection.Register(mux, connection.Deps{
		Pool: pool, Logger: logger, List: connections,
		CheckInterval: cfg.CheckInterval, ConnectionTimeout: cfg.ConnectionTimeout,
	})
	machines.Register(mux, machines.Deps{Pool: pool, Logger: logger, List: connections})
	web.Register(mux)
	if cfg.APIDocs {
		apidocs.Register(mux)
	}

	return requireKnownHost(cfg, mux)
}

// Run opens the database, checks its migration version, and serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.APIDocs {
		logger.Warn("API docs are on at /docs; turn them off outside development")
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.CheckVersion(ctx, pool); err != nil {
		return err
	}
	pairing.StartCleanup(ctx, pool, logger)
	connections := connection.NewList()
	defer connections.CloseAll()

	listener, err := net.Listen("tcp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("listen on server address: %w", err)
	}
	server := &http.Server{Addr: cfg.ServerAddr, Handler: New(cfg, pool, logger, connections)}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	logger.Info("server listening", "address", cfg.ServerAddr)

	select {
	case err := <-serveErr:
		connections.CloseAll()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		connections.CloseAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			_ = server.Close()
		}
		serveErrValue := <-serveErr
		connections.CloseAll()
		if shutdownErr != nil {
			return fmt.Errorf("shut down HTTP server: %w", shutdownErr)
		}
		if serveErrValue != nil && !errors.Is(serveErrValue, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", serveErrValue)
		}
		return nil
	}
}
