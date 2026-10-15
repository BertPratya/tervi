package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/state"
)

type localDisconnectResult uint8

const (
	localDisconnectUnknown localDisconnectResult = iota
	localDisconnectChanged
	localDisconnectAlready
	localDisconnectRejected
	localDisconnectRevoked
	localDisconnectNotPaired
	localDisconnectWriteFailed
	localDisconnectReadFailed
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
	result := localDisconnectUnknown
	err = control.WithLock(runtimeDir, func() error {
		record, exists, readErr := state.ReadState(env.StateDir)
		if readErr != nil {
			result = localDisconnectReadFailed
			return nil
		}
		if !exists || !record.Confirmed {
			result = localDisconnectNotPaired
			return nil
		}
		if record.StopReason != nil {
			switch *record.StopReason {
			case state.StopReasonCredentialRejected:
				result = localDisconnectRejected
				return nil
			case state.StopReasonCredentialRevoked:
				result = localDisconnectRevoked
				return nil
			}
		}
		if !record.Connect {
			result = localDisconnectAlready
			return nil
		}
		record.Connect = false
		record.StopReason = nil
		if writeErr := state.WriteState(env.StateDir, record); writeErr != nil {
			result = localDisconnectWriteFailed
			return nil
		}
		result = localDisconnectChanged
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
	switch result {
	case localDisconnectChanged:
		printDisconnectedWithoutDaemon(env.Stdout)
		return 0
	case localDisconnectAlready:
		fmt.Fprintln(env.Stdout, "Already disconnected.")
		return 0
	case localDisconnectRejected:
		printRejectedStatus(env.Stdout, entry.Server)
		return 0
	case localDisconnectRevoked:
		printUnpaired(env.Stdout, entry.Server)
		return 0
	case localDisconnectNotPaired:
		printConnectNotPaired(env.Stdout)
		return 1
	case localDisconnectWriteFailed:
		printConnectWriteFailure(env.Stdout, state.StatePath(env.StateDir), entry)
		return 1
	case localDisconnectReadFailed:
		printStateReadFailure(env.Stdout, env.StateDir)
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
	default:
		printStatusControlFailure(output, errors.New("unexpected daemon response"), entry)
		return 1
	}
}

func printDisconnectedWithoutDaemon(output io.Writer) {
	fmt.Fprintln(output, "✓ Disconnected. tervi won't connect, including after the computer restarts.")
	fmt.Fprintln(output, "  Connect again with: tervi connect")
}
