// Command server runs the tervi central server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bertpratya/tervi/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := server.LoadConfig()
	if err != nil {
		logger.Error("load server config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, cfg, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
