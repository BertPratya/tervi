package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/state"
)

func runStatus(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}
	if env.ControlClient == nil {
		return missingSetting(env.Stdout, "ControlClient")
	}
	entry, _, paired, err := credential.Paired(env.StateDir, env.Store)
	if err != nil {
		printPairingReadFailure(env.Stdout, env.StateDir, err)
		return 1
	}
	if !paired {
		printStatusNotPaired(env.Stdout)
		return 1
	}
	answer, err := env.ControlClient.Status(ctx)
	if ctx.Err() != nil {
		return 130
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		if errors.Is(err, control.ErrNotRunning) {
			printNoDaemonStatus(env.Stdout, env.StateDir, entry)
			return 1
		}
		printStatusControlFailure(env.Stdout, err, entry)
		return 1
	}

	switch answer.State {
	case daemon.StateConnected:
		printConnectedStatus(env.Stdout, answer, entry)
		return 0
	case daemon.StateReconnecting:
		printReconnectingStatus(env.Stdout, answer, entry)
		return 1
	case daemon.StateNotConnecting:
		if answer.StopReason != nil {
			switch *answer.StopReason {
			case state.StopReasonCredentialRejected:
				printRejectedStatus(env.Stdout, entry.Server)
				return 1
			case state.StopReasonCredentialRevoked:
				printUnpairedStatus(env.Stdout, entry.Server)
				return 1
			default:
				fmt.Fprintln(env.Stdout, "○ Not connecting.")
				fmt.Fprintln(env.Stdout, "  Connect with: tervi connect")
				return 1
			}
		}
		printDisconnectedStatus(env.Stdout)
		return 1
	case daemon.StateNotPaired:
		printStatusNotPaired(env.Stdout)
		return 1
	default:
		printStatusControlFailure(env.Stdout, fmt.Errorf("unexpected daemon status"), entry)
		return 1
	}
}

func printStatusNotPaired(output io.Writer) {
	fmt.Fprintln(output, "✗ This computer isn't paired.")
	fmt.Fprintln(output, "  Pair it with: tervi pair --server <url>")
}

func printStatusControlFailure(output io.Writer, err error, entry credential.Entry) {
	fmt.Fprintf(output, "✗ Can't reach tervi in the background: %s\n", redactText(err.Error(), entry))
}

func printRejectedStatus(output io.Writer, server string) {
	fmt.Fprintln(output, "✗ Not connecting: the server doesn't accept this computer's credential.")
	fmt.Fprintf(output, "  Pair again with: tervi pair --server %s\n", server)
}

func printUnpairedStatus(output io.Writer, server string) {
	fmt.Fprintln(output, "✗ This computer was unpaired on the server.")
	fmt.Fprintf(output, "  Pair it again with: tervi pair --server %s\n", server)
}

func printDisconnectedStatus(output io.Writer) {
	fmt.Fprintln(output, "○ Disconnected.")
	fmt.Fprintln(output, "  Connect with: tervi connect")
}

func printNoDaemonStatus(output io.Writer, stateDir string, entry credential.Entry) {
	record, exists, err := state.ReadState(stateDir)
	if err == nil && exists {
		if record.StopReason != nil {
			switch *record.StopReason {
			case state.StopReasonCredentialRejected:
				printRejectedStatus(output, entry.Server)
				return
			case state.StopReasonCredentialRevoked:
				printUnpairedStatus(output, entry.Server)
				return
			}
		}
		if !record.Connect && record.StopReason == nil {
			printDisconnectedStatus(output)
			return
		}
	}
	fmt.Fprintln(output, "✗ tervi isn't running in the background.")
	fmt.Fprintln(output, "  Start it with: tervi connect")
}

func printConnectedStatus(output io.Writer, answer control.StatusAnswer, entry credential.Entry) {
	machine, err := os.Hostname()
	if err != nil {
		machine = "unknown"
	}
	since, err := time.Parse(time.RFC3339, answer.Since)
	if err != nil {
		fmt.Fprintf(output, "● Connected to %s\n  Machine: %s\n  Since:   unknown\n", redactText(answer.Server, entry), machine)
		return
	}
	duration := time.Since(since)
	if duration < 0 {
		duration = 0
	}
	fmt.Fprintf(output, "● Connected to %s\n  Machine: %s\n  Since:   %s (%s)\n", redactText(answer.Server, entry), machine, since.Local().Format("15:04"), sinceText(duration))
}

func printReconnectingStatus(output io.Writer, answer control.StatusAnswer, entry credential.Entry) {
	server := answer.Server
	if server == "" {
		server = entry.Server
	}
	fmt.Fprintf(output, "○ Reconnecting to %s\n", redactText(server, entry))
	if answer.NextTryInSeconds == 0 {
		fmt.Fprintf(output, "  Try %d, trying now\n", answer.Try)
	} else {
		fmt.Fprintf(output, "  Try %d, next in %s\n", answer.Try, daemon.SecondsText(answer.NextTryInSeconds))
	}
	if answer.LastError != nil && *answer.LastError != "" {
		fmt.Fprintf(output, "  Last error: %s\n", redactText(*answer.LastError, entry))
	}
}

func sinceText(duration time.Duration) string {
	seconds := int64(duration / time.Second)
	switch {
	case seconds < 60:
		return "less than a minute"
	case seconds < 3600:
		minutes := seconds / 60
		return pluralCount(minutes, "minute")
	case seconds < 86400:
		hours := seconds / 3600
		return pluralCount(hours, "hour")
	default:
		days := seconds / 86400
		return pluralCount(days, "day")
	}
}

func pluralCount(count int64, unit string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", count, unit)
}
