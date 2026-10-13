package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/service"
)

const installRequiredMessage = "tervi must be installed to run in the background. Install it with: go install ./cmd/tervi"

func runConnect(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}
	if service.IsTemporaryProgram(env.ProgramPath, os.TempDir()) {
		fmt.Fprintln(env.Stdout, installRequiredMessage)
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
		return printConnectCancelled(env.Stdout)
	}

	if err := ensureRunning(ctx, env); err != nil {
		return handleEnsureRunningFailure(ctx, env.Stdout, err, entry, printConnectCancelled)
	}

	answer, err := env.ControlClient.Connect(ctx)
	return handleConnectResult(ctx, env.Stdout, answer, err, entry, connectResultBehavior{
		alreadyConnectedMessage: "Already connected.",
		stillTryingPrefix:       "Not connected yet",
		stillTryingExitCode:     1,
		printCancelled:          printConnectCancelled,
	})
}

type missingServiceSettingError struct{ field string }

func (err *missingServiceSettingError) Error() string { return err.field + " is missing" }

func ensureRunning(ctx context.Context, env Env) error {
	for _, setting := range []struct {
		field string
		valid bool
	}{
		{field: "ProgramPath", valid: env.ProgramPath != ""},
		{field: "Runner", valid: env.Runner != nil},
		{field: "ServiceDir", valid: env.ServiceDir != ""},
		{field: "ControlClient", valid: env.ControlClient != nil},
	} {
		if !setting.valid {
			return &missingServiceSettingError{field: setting.field}
		}
	}
	manager := service.New(service.Options{
		Runner: env.Runner, ProgramPath: env.ProgramPath, ServiceDir: env.ServiceDir, Client: env.ControlClient,
	})
	return manager.EnsureRunning(ctx)
}

func missingSetting(output io.Writer, name string) int {
	fmt.Fprintf(output, "✗ tervi is missing an internal setting (%s).\n", name)
	return 1
}

func handleEnsureRunningFailure(ctx context.Context, output io.Writer, err error, entry credential.Entry, printCancelled func(io.Writer) int) int {
	var missing *missingServiceSettingError
	if errors.As(err, &missing) {
		return missingSetting(output, missing.field)
	}
	if ctx.Err() != nil {
		return printCancelled(output)
	}
	printServiceStartFailure(output, err, entry)
	return 1
}

func printPairingReadFailure(output io.Writer, stateDir string, err error) {
	if errors.Is(err, credential.ErrStateUnreadable) {
		printStateReadFailure(output, stateDir)
		return
	}
	printStoreReadFailure(output)
}

func printConnectNotPaired(output io.Writer) {
	fmt.Fprintln(output, "This computer isn't paired. Pair it with: tervi pair --server <url>")
}

func printConnectRejected(output io.Writer, server string) {
	fmt.Fprintln(output, "✗ The server doesn't accept this computer's credential.")
	fmt.Fprintf(output, "Pair again with: tervi pair --server %s\n", server)
}

func printServiceStartFailure(output io.Writer, err error, entry credential.Entry) {
	fmt.Fprintf(output, "✗ Can't start tervi in the background: %s\n", redactText(err.Error(), entry))
	fmt.Fprintln(output, "Try again with: tervi connect")
}

func printControlFailure(output io.Writer, err error, entry credential.Entry) {
	printStatusControlFailure(output, err, entry)
	fmt.Fprintln(output, "Check with: tervi status")
}

func printStillTrying(output io.Writer, lastError *string, entry credential.Entry, prefix string) {
	if lastError != nil && *lastError != "" {
		fmt.Fprintf(output, "%s: %s. tervi keeps trying in the background.\n", prefix, redactText(*lastError, entry))
	} else {
		fmt.Fprintf(output, "%s. tervi keeps trying in the background.\n", prefix)
	}
	fmt.Fprintln(output, "Check with: tervi status")
}

func printConnectWriteFailure(output io.Writer, path string, entry credential.Entry) {
	fmt.Fprintf(output, "✗ Can't write %s.\n", redactText(path, entry))
}

type connectResultBehavior struct {
	alreadyConnectedMessage string
	stillTryingPrefix       string
	stillTryingExitCode     int
	printCancelled          func(io.Writer) int
}

func handleConnectResult(ctx context.Context, output io.Writer, answer control.ConnectAnswer, err error, entry credential.Entry, behavior connectResultBehavior) int {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return behavior.printCancelled(output)
	}
	if err != nil {
		printControlFailure(output, err, entry)
		return 1
	}
	switch answer.Result {
	case daemon.ResultConnected:
		fmt.Fprintln(output, "✓ Connected.")
		return 0
	case daemon.ResultAlreadyConnected:
		message := behavior.alreadyConnectedMessage
		if message == "" {
			message = "✓ Connected."
		}
		fmt.Fprintln(output, message)
		return 0
	case daemon.ResultStillTrying:
		printStillTrying(output, answer.LastError, entry, behavior.stillTryingPrefix)
		return behavior.stillTryingExitCode
	case daemon.ResultFailed:
		printConnectWriteFailure(output, answer.Path, entry)
		return 1
	case daemon.ResultRejected:
		printConnectRejected(output, entry.Server)
		return 1
	case daemon.ResultNotPaired:
		printConnectNotPaired(output)
		return 1
	default:
		printControlFailure(output, errors.New("unexpected daemon response"), entry)
		return 1
	}
}

func printConnectCancelled(output io.Writer) int {
	fmt.Fprintln(output, "Connecting was cancelled; tervi keeps trying in the background.")
	return 130
}

func redactText(text string, entry credential.Entry) string {
	secret := entry.Credential.Reveal()
	if secret != "" {
		return strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}

var _ service.ControlClient = (ControlClient)(nil)
