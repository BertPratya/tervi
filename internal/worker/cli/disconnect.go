package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
)

func runDisconnect(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}
	entry, _, paired, err := credential.Paired(env.StateDir, env.Store)
	if err != nil {
		printPairingReadFailure(env.Stdout, env.StateDir, err)
		return 1
	}
	if !paired {
		printConnectNotPaired(env.Stdout)
		return 1
	}
	if ctx.Err() != nil {
		return 130
	}
	runtimeDir, err := control.RuntimeDir()
	if err != nil {
		return 1
	}
	var result daemon.DisconnectResult
	err = control.WithLock(runtimeDir, func() error {
		result = daemon.DisconnectState(env.StateDir, env.Store)
		return nil
	})
	if errors.Is(err, control.ErrAlreadyRunning) {
		if env.ControlClient == nil {
			return missingSetting(env.Stdout, "ControlClient")
		}
		answer, requestErr := env.ControlClient.Disconnect(ctx)
		return handleDisconnectAnswer(ctx, env.Stdout, answer, requestErr, entry)
	}
	if err != nil {
		printStatusControlFailure(env.Stdout, err, entry)
		return 1
	}
	switch result.Result {
	case daemon.ResultDisconnected:
		printDisconnectedWithoutDaemon(env.Stdout)
		return 0
	case daemon.ResultAlreadyDisconnected:
		fmt.Fprintln(env.Stdout, "Already disconnected.")
		return 0
	case daemon.ResultRejected:
		printRejectedStatus(env.Stdout, entry.Server)
		return 0
	case daemon.ResultRevoked:
		printUnpaired(env.Stdout, entry.Server)
		return 0
	case daemon.ResultNotPaired:
		printConnectNotPaired(env.Stdout)
		return 1
	case daemon.ResultFailed:
		printConnectWriteFailure(env.Stdout, result.Path, entry)
		return 1
	case daemon.ResultUnreadable:
		printDisconnectReadFailure(env.Stdout, result.Path)
		return 1
	default:
		printStatusControlFailure(env.Stdout, errors.New("unexpected lock result"), entry)
		return 1
	}
}

func handleDisconnectAnswer(ctx context.Context, output io.Writer, answer control.DisconnectAnswer, err error, entry credential.Entry) int {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil {
		printStatusControlFailure(output, err, entry)
		return 1
	}
	switch answer.Result {
	case daemon.ResultDisconnected:
		fmt.Fprintln(output, "✓ Disconnected. tervi stays in the background but won't connect.")
		fmt.Fprintln(output, "  Connect again with: tervi connect")
		return 0
	case daemon.ResultAlreadyDisconnected:
		fmt.Fprintln(output, "Already disconnected.")
		return 0
	case daemon.ResultRejected:
		printRejectedStatus(output, entry.Server)
		return 0
	case daemon.ResultRevoked:
		printUnpaired(output, entry.Server)
		return 0
	case daemon.ResultNotPaired:
		printConnectNotPaired(output)
		return 1
	case daemon.ResultFailed:
		printConnectWriteFailure(output, answer.Path, entry)
		return 1
	case daemon.ResultUnreadable:
		printDisconnectReadFailure(output, answer.Path)
		return 1
	default:
		printStatusControlFailure(output, errors.New("unexpected daemon response"), entry)
		return 1
	}
}

func printDisconnectedWithoutDaemon(output io.Writer) {
	fmt.Fprintln(output, "✓ Disconnected. tervi won't connect, including after the computer restarts.")
	fmt.Fprintln(output, "  Connect again with: tervi connect")
}

func printDisconnectReadFailure(output io.Writer, path string) {
	fmt.Fprintf(output, "✗ Can't read %s.\n", path)
}
