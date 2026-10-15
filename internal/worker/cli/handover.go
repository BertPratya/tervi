package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const backgroundStartedMessage = "✓ tervi now runs in the background and starts when the computer starts."

// StartNewPairing is returned by FinishEarlier when the server revoked the
// earlier credential while its acknowledgment was in flight. It is a flow
// instruction, never a process exit code.
const StartNewPairing = -1

func runPairStart(ctx context.Context, env Env, flow Flow, server string) int {
	var entry credential.Entry
	flowEnv := env
	flowEnv.OnPairingSuccess = func(saved credential.Entry) { entry = saved }
	if code := flow.StartNew(ctx, flowEnv, server); code != 0 {
		return code
	}
	return handOver(ctx, env, entry)
}

func runPairFinish(ctx context.Context, env Env, flow Flow, entry credential.Entry, server string, yes bool) int {
	if code := flow.FinishEarlier(ctx, env, entry); code != 0 {
		if code == StartNewPairing {
			return runRevokedRePair(ctx, env, flow, server, yes)
		}
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

func runRejectedRePair(ctx context.Context, env Env, flow Flow, server string, yes bool) int {
	return runRePair(ctx, env, flow, server, yes, "The server no longer accepts this computer's credential. Starting a new pairing.")
}

func runStoppedRePair(ctx context.Context, env Env, flow Flow, server string, yes bool, reason string) (int, bool) {
	switch reason {
	case state.StopReasonCredentialRejected:
		return runRejectedRePair(ctx, env, flow, server, yes), true
	case state.StopReasonCredentialRevoked:
		return runRevokedRePair(ctx, env, flow, server, yes), true
	default:
		return 0, false
	}
}

func runRevokedRePair(ctx context.Context, env Env, flow Flow, server string, yes bool) int {
	return runRePair(ctx, env, flow, server, yes, "This computer was unpaired on the server. Starting a new pairing.")
}

func runRePair(ctx context.Context, env Env, flow Flow, server string, yes bool, message string) int {
	if !confirmMachineID(env, flow, yes) {
		return 1
	}
	fmt.Fprintln(env.Stdout, message)
	if code, ok := deletePairing(ctx, env, true, true); !ok {
		return code
	}
	return checkAndStart(ctx, env, flow, server)
}
