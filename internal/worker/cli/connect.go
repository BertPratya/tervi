package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/service"
)

const installRequiredMessage = "tervi must be installed to run in the background. Install it with: go install ./cmd/tervi"

func runConnect(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if env.ProgramPath == "" {
		return missingSetting(env.Stdout, "ProgramPath")
	}
	if env.Runner == nil {
		return missingSetting(env.Stdout, "Runner")
	}
	if env.ServiceDir == "" {
		return missingSetting(env.Stdout, "ServiceDir")
	}
	if env.ControlClient == nil {
		return missingSetting(env.Stdout, "ControlClient")
	}
	if isTemporaryProgram(env.ProgramPath, os.TempDir()) {
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

	status, statusErr := env.ControlClient.Status(ctx)
	if statusErr == nil && status.State == "connected" {
		fmt.Fprintln(env.Stdout, "Already connected.")
		return 0
	}
	if statusErr == nil && status.State == "not_connecting" && status.StopReason != nil && *status.StopReason == "credential_rejected" {
		printConnectRejected(env.Stdout, entry.Server)
		return 1
	}
	if ctx.Err() != nil {
		return printConnectCancelled(env.Stdout)
	}

	manager := service.New(service.Options{
		Runner: env.Runner, ProgramPath: env.ProgramPath, ServiceDir: env.ServiceDir, Client: env.ControlClient,
	})
	if err := manager.EnsureRunning(ctx); err != nil {
		if ctx.Err() != nil {
			return printConnectCancelled(env.Stdout)
		}
		printServiceStartFailure(env.Stdout, err, entry)
		return 1
	}

	answer, err := env.ControlClient.Connect(ctx)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return printConnectCancelled(env.Stdout)
		}
		printControlFailure(env.Stdout, err, entry)
		return 1
	}
	if ctx.Err() != nil {
		return printConnectCancelled(env.Stdout)
	}
	switch answer.Result {
	case "connected":
		fmt.Fprintln(env.Stdout, "✓ Connected.")
		return 0
	case "already_connected":
		fmt.Fprintln(env.Stdout, "Already connected.")
		return 0
	case "still_trying":
		printStillTrying(env.Stdout, answer.LastError, entry)
		return 1
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
		printControlFailure(env.Stdout, fmt.Errorf("unexpected daemon response"), entry)
		return 1
	}
}

func isTemporaryProgram(programPath, tempDir string) bool {
	if programPath == "" {
		return false
	}
	resolved, err := filepath.Abs(programPath)
	if err == nil {
		programPath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(programPath); err == nil {
		programPath = resolved
	}
	programPath = filepath.Clean(programPath)
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	if root, err := filepath.Abs(tempDir); err == nil && pathIsWithin(root, programPath) {
		return true
	}
	for _, folder := range strings.Split(programPath, string(os.PathSeparator)) {
		if folder == "go-build" {
			return true
		}
	}
	return false
}

func pathIsWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func missingSetting(output io.Writer, name string) int {
	fmt.Fprintf(output, "✗ tervi is missing an internal setting (%s).\n", name)
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
	fmt.Fprintf(output, "✗ Can't reach tervi in the background: %s\n", redactText(err.Error(), entry))
	fmt.Fprintln(output, "Check with: tervi status")
}

func printStillTrying(output io.Writer, lastError *string, entry credential.Entry) {
	if lastError != nil && *lastError != "" {
		fmt.Fprintf(output, "Not connected yet: %s. tervi keeps trying in the background.\n", redactText(*lastError, entry))
	} else {
		fmt.Fprintln(output, "Not connected yet. tervi keeps trying in the background.")
	}
	fmt.Fprintln(output, "Check with: tervi status")
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
