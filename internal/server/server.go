package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bertpratya/tervi/internal/db"
	"github.com/bertpratya/tervi/internal/pairing"
	"github.com/bertpratya/tervi/internal/web"
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

	allowedHosts := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
	}
	if publicURL, err := url.Parse(cfg.PublicURL); err == nil {
		if host := strings.ToLower(publicURL.Hostname()); host != "" {
			allowedHosts[host] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(requestHostName(r.Host))
		if _, ok := allowedHosts[host]; !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error": "unknown_host"}`))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func requestHostName(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
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
