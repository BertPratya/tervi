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
	"github.com/bertpratya/tervi/internal/worker/service"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const usageLine = "Usage: tervi pair --server <url>\n       tervi connect\n       tervi disconnect\n       tervi status\n       tervi logs [-f]\n       tervi daemon"

// Env supplies the worker's local inputs, outputs, and state locations.
type Env struct {
	Stdin            io.Reader
	Stdout, Stderr   io.Writer
	Store            *credential.Store
	StateDir         string
	Runner           service.Runner
	ProgramPath      string
	ServiceDir       string
	ControlClient    ControlClient
	OnPairingSuccess func(credential.Entry)
}

// ControlClient is the private client used by commands to talk to the daemon.
type ControlClient interface {
	Status(ctx context.Context) (control.StatusAnswer, error)
	Connect(ctx context.Context) (control.ConnectAnswer, error)
	Disconnect(ctx context.Context) (control.DisconnectAnswer, error)
}

func printUnpaired(output io.Writer, server string) {
	fmt.Fprintln(output, "✗ This computer was unpaired on the server.")
	fmt.Fprintf(output, "  Pair it again with: tervi pair --server %s\n", server)
}

// Flow runs the network portion of a new or resumed pairing.
type Flow interface {
	StartNew(ctx context.Context, env Env, server string) int
	FinishEarlier(ctx context.Context, env Env, entry credential.Entry) int
}

type command struct{ server string }

func parseCommand(args []string) (command, string, bool) {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return command{}, "", true
		}
	}
	if len(args) == 0 {
		return command{}, "", true
	}
	if args[0] != "pair" {
		return command{}, "Unknown command: " + args[0], false
	}
	seenServer := false
	server := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--server" {
			if seenServer {
				return command{}, "", true
			}
			seenServer = true
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return command{}, "", true
			}
			i++
			server = args[i]
		} else if strings.HasPrefix(arg, "--server=") {
			if seenServer {
				return command{}, "", true
			}
			seenServer = true
			server = strings.TrimPrefix(arg, "--server=")
		} else if strings.HasPrefix(arg, "-") {
			return command{}, "Unknown flag: " + arg, false
		} else {
			return command{}, "Unknown argument: " + arg, false
		}
		if server == "" {
			return command{}, "", true
		}
		standard, err := state.StandardAddress(server)
		if err != nil {
			return command{}, "Invalid server address: " + server, false
		}
		server = standard
	}
	if !seenServer {
		return command{}, "", true
	}
	return command{server: server}, "", false
}

// Run parses the command, resolves step zero, and delegates network work to flow.
func Run(ctx context.Context, args []string, env Env, flow Flow) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(args) > 0 {
		switch args[0] {
		case "daemon":
			if len(args) != 1 {
				fmt.Fprintln(env.Stderr, usageLine)
				return 2
			}
			return runDaemon(ctx, env)
		case "connect":
			return runConnect(ctx, args[1:], env)
		case "disconnect":
			return runDisconnect(ctx, args[1:], env)
		case "status":
			return runStatus(ctx, args[1:], env)
		case "logs":
			return runLogs(ctx, args[1:], env)
		}
	}
	cmd, parseMessage, usageOnly := parseCommand(args)
	if usageOnly || parseMessage != "" {
		if parseMessage != "" {
			fmt.Fprintln(env.Stderr, parseMessage)
		}
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if env.ProgramPath == "" {
		return missingSetting(env.Stdout, "ProgramPath")
	}
	if service.IsTemporaryProgram(env.ProgramPath, os.TempDir()) {
		fmt.Fprintln(env.Stdout, installRequiredMessage)
		return 1
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}

	record, stateExists, err := state.ReadState(env.StateDir)
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	if err != nil {
		printStateReadFailure(env.Stdout, env.StateDir)
		return 1
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	var entry credential.Entry
	var entryExists bool
	var entryErr error
	if env.Store == nil {
		entryErr = errors.New("credential file unavailable")
	} else {
		entry, entryExists, entryErr = env.Store.Get()
	}
	candidate := entryErr == nil && entryExists && stateExists && !record.Confirmed && entry.Server == cmd.server
	if cancelled(ctx, env.Stdout, candidate, cmd.server) {
		return 130
	}
	if errors.Is(entryErr, credential.ErrUnprotected) {
		printUnprotectedCredential(env.Stdout, credential.Path(env.StateDir))
		return 1
	}
	if errors.Is(entryErr, credential.ErrDamaged) {
		return runLeftover(ctx, env, true, stateExists, fmt.Sprintf("Found a damaged %s.", credential.Path(env.StateDir)), true, cmd.server, flow)
	}
	if entryErr != nil {
		printStoreReadFailure(env.Stdout, credential.Path(env.StateDir))
		return 1
	}
	if stateExists && record.Confirmed && entryExists && record.StopReason != nil && *record.StopReason == state.StopReasonCredentialRejected {
		return runRejectedRePair(ctx, env, flow, cmd.server)
	}

	if !stateExists && !entryExists {
		return checkAndStart(ctx, env, flow, cmd.server)
	}
	if !stateExists && entryExists {
		return runLeftover(ctx, env, true, false, "Found incomplete pairing data: credential.json exists, but state.json is missing.", true, cmd.server, flow)
	}
	if stateExists && !record.CredentialSaved && !entryExists {
		return runLeftover(ctx, env, false, true, "The earlier pairing was interrupted before the credential was saved.", true, cmd.server, flow)
	}
	if stateExists && entryExists && !record.Confirmed {
		if entry.Server != cmd.server {
			fmt.Fprintf(env.Stdout, "✗ A pairing with %s isn't finished yet.\n  To finish it, run:  tervi pair --server %s\n", entry.Server, entry.Server)
			return 1
		}
		if !record.CredentialSaved {
			record.CredentialSaved = true
			if cancelled(ctx, env.Stdout, true, cmd.server) {
				return 130
			}
			if err := state.WriteState(env.StateDir, record); cancelled(ctx, env.Stdout, true, cmd.server) {
				return 130
			} else if err != nil {
				fmt.Fprintf(env.Stdout, "✗ Can't write %s.\n", state.StatePath(env.StateDir))
				return 1
			}
		}
		if cancelled(ctx, env.Stdout, true, cmd.server) {
			return 130
		}
		if flow == nil {
			return 1
		}
		return runPairFinish(ctx, env, flow, entry)
	}
	if stateExists && record.CredentialSaved && !entryExists {
		return runLeftover(ctx, env, false, true, "Found incomplete pairing data: state.json exists, but credential.json is missing.", true, cmd.server, flow)
	}
	if stateExists && record.Confirmed && entryExists {
		fmt.Fprintf(env.Stdout, "This computer is already paired with %s. Nothing was changed.\n", entry.Server)
		return 1
	}
	return 1
}

func cancelled(ctx context.Context, output io.Writer, resumable bool, server ...string) bool {
	if ctx.Err() == nil {
		return false
	}
	if resumable {
		fmt.Fprintln(output, "Pairing cancelled before it was confirmed.")
		if len(server) > 0 {
			fmt.Fprintf(output, "  To finish, run:  tervi pair --server %s\n", server[0])
		}
		return true
	}
	fmt.Fprintln(output, "Pairing cancelled. Nothing was saved.")
	return true
}

func printStoreReadFailure(output io.Writer, path string) {
	fmt.Fprintf(output, "✗ Can't read %s.\n", path)
}

func requireRuntimeFolder(output io.Writer) bool {
	if _, err := control.RuntimeDir(); errors.Is(err, control.ErrRuntimeDirNotSet) {
		fmt.Fprintln(output, "✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).")
		return false
	}
	return true
}

func printStateReadFailure(output io.Writer, stateDir string) {
	fmt.Fprintf(output, "✗ Can't read %s.\n", state.StatePath(stateDir))
}

func printStoreDeleteFailure(output io.Writer, path string) {
	fmt.Fprintf(output, "✗ Can't delete %s.\n", path)
}

func printUnusableStore(output io.Writer, path string) {
	fmt.Fprintf(output, "✗ Can't write %s.\n", path)
}

func printUnprotectedCredential(output io.Writer, path string) {
	fmt.Fprintf(output, "✗ Other users may be able to read %s, so tervi won't use it.\n", path)
	fmt.Fprintf(output, "  Make it private with: chmod 600 %s\n", path)
}
