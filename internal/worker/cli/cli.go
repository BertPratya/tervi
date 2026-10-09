package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const usageLine = "Usage: tervi pair --server <url>"

// Env supplies the worker's local inputs, outputs, and state locations.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Store          *credential.Store
	StateDir       string
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
	cmd, parseMessage, usageOnly := parseCommand(args)
	if usageOnly || parseMessage != "" {
		if parseMessage != "" {
			fmt.Fprintln(env.Stderr, parseMessage)
		}
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}

	record, stateExists, err := state.ReadState(env.StateDir)
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	if err != nil {
		fmt.Fprintf(env.Stdout, "✗ Can't read %s.\n", state.StatePath(env.StateDir))
		return 1
	}
	if cancelled(ctx, env.Stdout, false) {
		return 130
	}
	var entry credential.Entry
	var entryExists bool
	var entryErr error
	if env.Store == nil {
		entryErr = errors.New("secret store unavailable")
	} else {
		entry, entryExists, entryErr = env.Store.Get()
	}
	candidate := entryErr == nil && entryExists && stateExists && !record.Confirmed && entry.Server == cmd.server
	if cancelled(ctx, env.Stdout, candidate, cmd.server) {
		return 130
	}
	if errors.Is(entryErr, credential.ErrDamaged) {
		return runLeftover(ctx, env, true, stateExists, "Found a damaged secret store entry for tervi.", true, cmd.server, flow)
	}
	if entryErr != nil {
		printStoreReadFailure(env.Stdout)
		return 1
	}

	if !stateExists && !entryExists {
		return checkAndStart(ctx, env, flow, cmd.server)
	}
	if !stateExists && entryExists {
		return runLeftover(ctx, env, true, false, "Found incomplete pairing data: the secret store entry exists, but state.json is missing.", true, cmd.server, flow)
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
		return flow.FinishEarlier(ctx, env, entry)
	}
	if stateExists && record.CredentialSaved && !entryExists {
		return runLeftover(ctx, env, false, true, "Found incomplete pairing data: state.json exists, but the secret store entry is missing.", true, cmd.server, flow)
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

func printStoreReadFailure(output io.Writer) {
	fmt.Fprintln(output, "✗ Can't read the secret store entry.")
	fmt.Fprintln(output, "  Make sure you are logged in to a desktop session and the keyring is unlocked.")
}

func printStoreDeleteFailure(output io.Writer) {
	fmt.Fprintln(output, "✗ Can't delete the secret store entry.")
	fmt.Fprintln(output, "  Make sure you are logged in to a desktop session and the keyring is unlocked.")
}

func printUnusableStore(output io.Writer) {
	fmt.Fprintln(output, "✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.")
	fmt.Fprintln(output, "  Make sure you are logged in to a desktop session and the keyring is unlocked.")
}
