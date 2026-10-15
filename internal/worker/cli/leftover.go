package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/credential"
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
		printUnusableStore(env.Stdout, credential.Path(env.StateDir))
		return 1
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	if flow == nil {
		return 1
	}
	return runPairStart(ctx, env, flow, server)
}

func runLeftover(ctx context.Context, env Env, entryExists, stateExists bool, message string, start bool, server string, flow Flow, yes bool) int {
	if start && !confirmMachineID(env, flow, yes) {
		return 1
	}
	if code, ok := deletePairing(ctx, env, entryExists, stateExists); !ok {
		return code
	}
	fmt.Fprintf(env.Stdout, "%s\n  Removed the leftover pairing data. Starting a new pairing.\n", message)
	if !start {
		return 0
	}
	return checkAndStart(ctx, env, flow, server)
}

func deletePairing(ctx context.Context, env Env, entryExists, stateExists bool) (code int, ok bool) {
	if entryExists {
		if cancelled(ctx, env.Stdout, false) {
			return 130, false
		}
		if env.Store == nil || env.Store.Delete() != nil {
			if cancelled(ctx, env.Stdout, false) {
				return 130, false
			}
			printStoreDeleteFailure(env.Stdout, credential.Path(env.StateDir))
			return 1, false
		}
		if cancelled(ctx, env.Stdout, false) {
			return 130, false
		}
	}
	if stateExists {
		if cancelled(ctx, env.Stdout, false) {
			return 130, false
		}
		if err := state.DeleteState(env.StateDir); cancelled(ctx, env.Stdout, false) {
			return 130, false
		} else if err != nil {
			printStateDeleteFailure(env.Stdout, env.StateDir)
			return 1, false
		}
	}
	return 0, true
}

func printStateDeleteFailure(output io.Writer, stateDir string) {
	fmt.Fprintf(output, "✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", state.StatePath(stateDir), stateDir)
}
