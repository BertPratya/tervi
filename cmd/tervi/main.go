package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/bertpratya/tervi/internal/worker/flow"
	"github.com/bertpratya/tervi/internal/worker/keyring"
	"github.com/bertpratya/tervi/internal/worker/local"
)

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stdout, "✗ Can't find this user's configuration folder.")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	env := local.Env{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Store:    local.NewStore(keyring.New()),
		StateDir: filepath.Join(configDir, "tervi"),
	}
	workerFlow := flow.New(flow.Options{
		RequestTimeout: 10 * time.Second,
		PollInterval:   2 * time.Second,
		Now:            time.Now,
	})
	code := local.Run(ctx, os.Args[1:], env, workerFlow)
	stop()
	os.Exit(code)
}
