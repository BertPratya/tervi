package cli

import (
	"context"
	"fmt"

	"github.com/bertpratya/tervi/internal/worker/state"
)

func checkAndStart(ctx context.Context, env Env, flow Flow, server string) int {
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	if env.Store == nil || env.Store.CheckContext(ctx) != nil {
		if cancelled(ctx, env.Stdout, false) {
			return 130
		}
		printUnusableStore(env.Stdout)
		return 1
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	if flow == nil {
		return 1
	}
	return flow.StartNew(ctx, env, server)
}

func runLeftover(ctx context.Context, env Env, entryExists, stateExists bool, message string, start bool, server string, flow Flow) int {
	if entryExists {
		if cancelled(ctx, env.Stdout, false) {
			return 130
		}
		if env.Store == nil || env.Store.Delete() != nil {
			if cancelled(ctx, env.Stdout, false) {
				return 130
			}
			printStoreDeleteFailure(env.Stdout)
			return 1
		}
		if cancelled(ctx, env.Stdout, false) {
			return 130
		}
	}
	if stateExists {
		if cancelled(ctx, env.Stdout, false) {
			return 130
		}
		if err := state.DeleteState(env.StateDir); cancelled(ctx, env.Stdout, false) {
			return 130
		} else if err != nil {
			fmt.Fprintf(env.Stdout, "✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", state.StatePath(env.StateDir), env.StateDir)
			return 1
		}
	}
	fmt.Fprintf(env.Stdout, "%s\n  Removed the leftover pairing data. Starting a new pairing.\n", message)
	if !start {
		return 0
	}
	return checkAndStart(ctx, env, flow, server)
}
