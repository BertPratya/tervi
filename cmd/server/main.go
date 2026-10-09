// Command server runs the tervi central server.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "migrate" {
		return runMigrate(ctx, stdout, stderr)
	}
	if len(args) != 0 {
		_, _ = io.WriteString(stderr, "Usage: server [migrate]\n")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := server.LoadConfig()
	if err != nil {
		logger.Error("load server config", "error", err)
		return 1
	}
	if err := server.Run(ctx, cfg, logger); err != nil {
		logger.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

func runMigrate(ctx context.Context, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	databaseURL, err := server.LoadDatabaseURL()
	if err != nil {
		logger.Error("migrate database", "error", err)
		return 1
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		logger.Error("migrate database", "error", err)
		return 1
	}
	defer pool.Close()

	version, err := db.Migrate(ctx, pool)
	if err != nil {
		logger.Error("migrate database", "error", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "Database is up to date (version %d).\n", version); err != nil {
		logger.Error("migrate database", "error", err)
		return 1
	}
	return 0
}
