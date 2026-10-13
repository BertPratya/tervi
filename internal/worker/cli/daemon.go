package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/daemon"
)

func runDaemon(ctx context.Context, env Env) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}
	runtimeDir, _ := control.RuntimeDir()
	engine := daemon.New(daemon.Options{
		StateDir: env.StateDir,
		Store:    env.Store,
		Log:      env.Stdout,
	})
	err := control.Run(ctx, runtimeDir, engine)
	if errors.Is(err, control.ErrAlreadyRunning) {
		fmt.Fprintln(env.Stdout, "tervi is already running in the background.")
		return 0
	}
	if err != nil {
		fmt.Fprintf(env.Stdout, "✗ tervi stopped unexpectedly: %v\n", err)
		return 1
	}
	return 0
}
