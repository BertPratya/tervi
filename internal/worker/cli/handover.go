package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/credential"
)

const backgroundStartedMessage = "✓ tervi now runs in the background and starts when you log in."

func runPairStart(ctx context.Context, env Env, flow Flow, server string) int {
	var entry credential.Entry
	flowEnv := env
	flowEnv.OnPairingSuccess = func(saved credential.Entry) { entry = saved }
	if code := flow.StartNew(ctx, flowEnv, server); code != 0 {
		return code
	}
	return handOver(ctx, env, entry)
}

func runPairFinish(ctx context.Context, env Env, flow Flow, entry credential.Entry) int {
	if code := flow.FinishEarlier(ctx, env, entry); code != 0 {
		return code
	}
	return handOver(ctx, env, entry)
}

func handOver(ctx context.Context, env Env, entry credential.Entry) int {
	if ctx.Err() != nil {
		return printPairHandoverCancelled(env.Stdout)
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}

	if _, err := ensureRunning(ctx, env); err != nil {
		return handleEnsureRunningFailure(ctx, env.Stdout, err, entry, printPairHandoverCancelled)
	}
	if ctx.Err() != nil {
		return printPairHandoverCancelled(env.Stdout)
	}
	fmt.Fprintln(env.Stdout, backgroundStartedMessage)

	answer, err := env.ControlClient.Connect(ctx)
	return handleConnectResult(ctx, env.Stdout, answer, err, entry, connectResultBehavior{
		stillTryingPrefix:   "Paired, but not connected yet",
		stillTryingExitCode: 0,
		printCancelled:      printPairHandoverCancelled,
	})
}

func printPairHandoverCancelled(output io.Writer) int {
	fmt.Fprintln(output, "Pairing finished. Connecting was cancelled; tervi keeps trying in the background.")
	return 130
}

func runRejectedRePair(ctx context.Context, env Env, flow Flow, server string) int {
	fmt.Fprintln(env.Stdout, "The server no longer accepts this computer's credential. Starting a new pairing.")
	if code, ok := deletePairing(ctx, env, true, true); !ok {
		return code
	}
	return checkAndStart(ctx, env, flow, server)
}
