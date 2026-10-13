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
	runtimeDir, err := control.RuntimeDir()
	if errors.Is(err, control.ErrRuntimeDirNotSet) {
		fmt.Fprintln(env.Stdout, "✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).")
		return 1
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "✗ Can't use this session's runtime folder.")
		return 1
	}
	engine := daemon.New(daemon.Options{
		StateDir: env.StateDir,
		Store:    env.Store,
		Log:      env.Stdout,
	})
	err = control.Run(ctx, runtimeDir, engine)
	if errors.Is(err, control.ErrAlreadyRunning) {
		fmt.Fprintln(env.Stdout, "tervi is already running in the background.")
		return 0
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "✗ The tervi daemon stopped unexpectedly.")
		return 1
	}
	return 0
}
