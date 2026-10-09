package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/pair"
)

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stdout, "✗ Can't find this user's configuration folder.")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	env := cli.Env{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Store:    credential.NewStore(credential.NewKeyringBackend()),
		StateDir: filepath.Join(configDir, "tervi"),
	}
	workerFlow := pair.New(pair.Options{
		RequestTimeout: 10 * time.Second,
		PollInterval:   2 * time.Second,
		Now:            time.Now,
	})
	code := cli.Run(ctx, os.Args[1:], env, workerFlow)
	stop()
	os.Exit(code)
}
