package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/service"
	"github.com/bertpratya/tervi/internal/worker/state"
)

// Unpairer does the work of tervi unpair; internal/worker/unpair implements it.
type Unpairer interface {
	// TellServer sends the unpair request once (step 1).
	TellServer(ctx context.Context, entry credential.Entry) UnpairAnswer
	// RemoveLocal does steps 2 and 3. A nil result means everything is gone.
	RemoveLocal(ctx context.Context, env Env) *RemoveFailure
	// LingerOn does step 4; false also means it could not tell.
	LingerOn(ctx context.Context, env Env) bool
}

// UnpairAnswer is the server's answer to the unpair request.
type UnpairAnswer struct {
	Done     bool // 204, or any 401: the server part is finished
	Answered bool // false when no answer came
	Status   int  // the HTTP status when Answered and not Done
}

// RemoveFailure says what could not be removed.
type RemoveFailure struct {
	Background bool
	Path       string
	Reason     string
}

type unpairOptions struct {
	yes       bool
	localOnly bool
}

func parseUnpairOptions(args []string) (unpairOptions, bool) {
	var options unpairOptions
	seenYes, seenLocalOnly := false, false
	for _, arg := range args {
		switch arg {
		case "--yes":
			if seenYes {
				return unpairOptions{}, false
			}
			seenYes = true
			options.yes = true
		case "--local-only":
			if seenLocalOnly {
				return unpairOptions{}, false
			}
			seenLocalOnly = true
			options.localOnly = true
		default:
			return unpairOptions{}, false
		}
	}
	return options, true
}

func runUnpair(ctx context.Context, args []string, env Env) int {
	options, valid := parseUnpairOptions(args)
	if !valid {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if !requireRuntimeFolder(env.Stdout) {
		return 1
	}
	if env.Unpairer == nil {
		return missingSetting(env.Stdout, "Unpairer")
	}
	if env.Store == nil {
		printStoreReadFailure(env.Stdout, credential.Path(env.StateDir))
		return 1
	}

	entry, credentialExists, credentialErr := env.Store.Get()
	usableCredential := credentialErr == nil && credentialExists
	leftoverCredential := errors.Is(credentialErr, credential.ErrDamaged) || errors.Is(credentialErr, credential.ErrUnprotected)
	if credentialErr != nil && !leftoverCredential {
		printStoreReadFailure(env.Stdout, credential.Path(env.StateDir))
		return 1
	}
	if !usableCredential {
		leftovers, err := localPairingRemains(env, leftoverCredential)
		if err != nil {
			if errors.Is(err, errStatePathUnreadable) {
				printStateReadFailure(env.Stdout, env.StateDir)
			} else {
				printStoreReadFailure(env.Stdout, service.ServiceFilePath(env.ServiceDir))
			}
			return 1
		}
		if !leftovers {
			fmt.Fprintln(env.Stdout, "This computer isn't paired. Nothing was changed.")
			return 1
		}
		if !options.yes {
			fmt.Fprintln(env.Stdout, "This removes what is left of an earlier pairing on this computer. Continue? [y/N]")
			if !confirmUnpair(env.Stdin) {
				fmt.Fprintln(env.Stdout, "Nothing was changed.")
				return 1
			}
		}
		if failure := env.Unpairer.RemoveLocal(ctx, env); failure != nil {
			return printUnpairRemovalFailure(env.Stdout, failure, credential.Entry{})
		}
		fmt.Fprintln(env.Stdout, "✓ Removed what was left of the earlier pairing.")
		printUnpairLinger(ctx, env)
		return 0
	}

	if !options.yes {
		if options.localOnly {
			fmt.Fprintf(env.Stdout, "This removes this computer's pairing without telling %s.\n", entry.Server)
			fmt.Fprintf(env.Stdout, "If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]\n")
		} else {
			fmt.Fprintf(env.Stdout, "This removes this computer's pairing with %s.\n", entry.Server)
			fmt.Fprintln(env.Stdout, "tervi will stop running in the background. Continue? [y/N]")
		}
		if !confirmUnpair(env.Stdin) {
			fmt.Fprintln(env.Stdout, "Nothing was changed.")
			return 1
		}
	}

	if !options.localOnly {
		answer := env.Unpairer.TellServer(ctx, entry)
		if !answer.Done {
			if !answer.Answered {
				fmt.Fprintf(env.Stdout, "✗ No answer from %s.\n", entry.Server)
				fmt.Fprintln(env.Stdout, "  The server may or may not have unpaired this computer. Nothing was removed here.")
				fmt.Fprintln(env.Stdout, "  Run tervi unpair again to finish, or revoke this computer on the Machines page.")
				return 1
			}
			fmt.Fprintf(env.Stdout, "✗ %s couldn't unpair this computer (HTTP %d). Nothing was removed here.\n", entry.Server, answer.Status)
			fmt.Fprintln(env.Stdout, "  Run tervi unpair again to finish, or revoke this computer on the Machines page.")
			return 1
		}
	}
	if failure := env.Unpairer.RemoveLocal(ctx, env); failure != nil {
		return printUnpairRemovalFailure(env.Stdout, failure, entry)
	}
	if options.localOnly {
		fmt.Fprintln(env.Stdout, "✓ Unpaired on this computer only.")
	} else {
		fmt.Fprintf(env.Stdout, "✓ Unpaired. This computer is no longer paired with %s.\n", entry.Server)
	}
	printUnpairLinger(ctx, env)
	return 0
}

var errStatePathUnreadable = errors.New("state path unreadable")

func localPairingRemains(env Env, credentialLeftover bool) (bool, error) {
	if credentialLeftover {
		return true, nil
	}
	stateExists, err := pathExists(state.StatePath(env.StateDir))
	if err != nil {
		return false, fmt.Errorf("%w: %v", errStatePathUnreadable, err)
	}
	if stateExists {
		return true, nil
	}
	if env.ServiceDir != "" {
		serviceExists, err := pathExists(service.ServiceFilePath(env.ServiceDir))
		if err != nil {
			return false, err
		}
		if serviceExists {
			return true, nil
		}
	}
	return credential.HasLeftovers(env.StateDir)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func confirmUnpair(input io.Reader) bool {
	answer, complete := readAnswer(input)
	return complete && isYes(answer)
}

func printUnpairRemovalFailure(output io.Writer, failure *RemoveFailure, entry credential.Entry) int {
	reason := redactText(failure.Reason, entry)
	if failure.Background {
		fmt.Fprintf(output, "✗ Can't stop tervi in the background: %s\n", reason)
	} else {
		fmt.Fprintf(output, "✗ Can't delete %s: %s\n", redactText(failure.Path, entry), reason)
	}
	fmt.Fprintln(output, "  Finish with: tervi unpair --local-only")
	return 1
}

func printUnpairLinger(ctx context.Context, env Env) {
	if env.Unpairer.LingerOn(ctx, env) {
		fmt.Fprintln(env.Stdout, "  Linger is still on. Turn it off with: loginctl disable-linger")
	}
}
