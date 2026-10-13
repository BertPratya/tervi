package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const backgroundStartedMessage = "✓ tervi now runs in the background and starts when you log in."

func runPairStart(ctx context.Context, env Env, flow Flow, server string) int {
	if flow == nil {
		return 1
	}
	if code := flow.StartNew(ctx, env, server); code != 0 {
		return code
	}
	return handOver(ctx, env, server, nil)
}

func runPairFinish(ctx context.Context, env Env, flow Flow, entry credential.Entry) int {
	if flow == nil {
		return 1
	}
	if code := flow.FinishEarlier(ctx, env, entry); code != 0 {
		return code
	}
	return handOver(ctx, env, entry.Server, &entry)
}

func handOver(ctx context.Context, env Env, server string, knownEntry *credential.Entry) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return printPairHandoverCancelled(env.Stdout)
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}

	var entry credential.Entry
	if knownEntry != nil {
		entry = *knownEntry
	} else {
		if env.Store == nil {
			printStoreReadFailure(env.Stdout)
			return 1
		}
		var exists bool
		var err error
		entry, exists, err = env.Store.Get()
		if err != nil {
			printStoreReadFailure(env.Stdout)
			return 1
		}
		if !exists {
			entry.Server = server
		}
	}

	if err := ensureRunning(ctx, env); err != nil {
		var missing *missingServiceSettingError
		if errors.As(err, &missing) {
			return missingSetting(env.Stdout, missing.field)
		}
		if ctx.Err() != nil {
			return printPairHandoverCancelled(env.Stdout)
		}
		printServiceStartFailure(env.Stdout, err, entry)
		return 1
	}
	if ctx.Err() != nil {
		return printPairHandoverCancelled(env.Stdout)
	}
	fmt.Fprintln(env.Stdout, backgroundStartedMessage)

	answer, err := env.ControlClient.Connect(ctx)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return printPairHandoverCancelled(env.Stdout)
	}
	if err != nil {
		printControlFailure(env.Stdout, err, entry)
		return 1
	}
	switch answer.Result {
	case "connected", "already_connected":
		fmt.Fprintln(env.Stdout, "✓ Connected.")
		return 0
	case "still_trying":
		printPairStillTrying(env.Stdout, answer.LastError, entry)
		return 0
	case "failed":
		fmt.Fprintf(env.Stdout, "✗ Can't write %s.\n", redactText(answer.Path, entry))
		return 1
	case "rejected":
		printConnectRejected(env.Stdout, entry.Server)
		return 1
	case "not_paired":
		printConnectNotPaired(env.Stdout)
		return 1
	default:
		printControlFailure(env.Stdout, errors.New("unexpected daemon response"), entry)
		return 1
	}
}

func printPairStillTrying(output io.Writer, lastError *string, entry credential.Entry) {
	if lastError != nil && *lastError != "" {
		fmt.Fprintf(output, "Paired, but not connected yet: %s. tervi keeps trying in the background.\n", redactText(*lastError, entry))
	} else {
		fmt.Fprintln(output, "Paired, but not connected yet. tervi keeps trying in the background.")
	}
	fmt.Fprintln(output, "Check with: tervi status")
}

func printPairHandoverCancelled(output io.Writer) int {
	fmt.Fprintln(output, "Pairing finished. Connecting was cancelled; tervi keeps trying in the background.")
	return 130
}

func runRejectedRePair(ctx context.Context, env Env, flow Flow, server string) int {
	fmt.Fprintln(env.Stdout, "The server no longer accepts this computer's credential. Starting a new pairing.")
	if flow == nil {
		return 1
	}
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
	if err := state.DeleteState(env.StateDir); cancelled(ctx, env.Stdout, false) {
		return 130
	} else if err != nil {
		printStateDeleteFailure(env.Stdout, env.StateDir)
		return 1
	}
	return checkAndStart(ctx, env, flow, server)
}
