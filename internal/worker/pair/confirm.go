package pair

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

func (f *pairingFlow) saveAndConfirm(ctx context.Context, env cli.Env, server string, answer codeAnswer) int {
	ctx = normalizeContext(ctx)
	entry := credential.Entry{Server: server, Credential: answer.Credential}
	record := state.State{MachineID: answer.MachineID}
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	if err := state.WriteState(env.StateDir, record); ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	} else if err != nil {
		fmt.Fprintf(output(env), "✗ Can't write %s. Pairing was not completed.\n", state.StatePath(env.StateDir))
		f.reportSaveFailure(ctx, server, entry.Credential)
		return 1
	}
	if ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	}
	if env.Store == nil {
		return f.saveFailed(ctx, env, entry)
	}
	if err := env.Store.Save(entry); ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	} else if err != nil {
		return f.saveFailed(ctx, env, entry)
	}
	record.CredentialSaved = true
	if ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	}
	if err := state.WriteState(env.StateDir, record); ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	} else if err != nil {
		fmt.Fprintf(output(env), "✗ Can't write %s.\n", state.StatePath(env.StateDir))
		printFinishCommand(env, server)
		return 1
	}
	fmt.Fprintln(output(env), "✓ Credential saved.")
	if ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	}
	return f.confirm(ctx, env, entry, false)
}

func (f *pairingFlow) saveFailed(ctx context.Context, env cli.Env, entry credential.Entry) int {
	fmt.Fprintln(output(env), "✗ Couldn't save the credential. Pairing was not completed.")
	f.reportSaveFailure(ctx, entry.Server, entry.Credential)
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	if env.Store == nil || env.Store.Delete() != nil {
		if ctx.Err() != nil {
			return cancelAfterSaving(env, entry.Server)
		}
		fmt.Fprintln(output(env), "✗ Couldn't remove the partly saved secret store entry.")
		fmt.Fprintln(output(env), "  Make sure you are logged in to a desktop session and the keyring is unlocked, then run the same command again.")
		return 1
	}
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	if err := state.DeleteState(env.StateDir); ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	} else if err != nil {
		printDeleteStateFailure(env)
		return 1
	}
	return 1
}

func (f *pairingFlow) reportSaveFailure(ctx context.Context, server string, machineCredential secret.Value) {
	if ctx.Err() != nil {
		return
	}
	_ = f.request(ctx, http.MethodPost, server+saveFailurePath, machineCredential.Reveal(), nil)
}

func (f *pairingFlow) confirm(ctx context.Context, env cli.Env, entry credential.Entry, earlier bool) int {
	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	fmt.Fprintln(output(env), "Confirming with the server...")
	for attempt := 1; attempt <= 3; attempt++ {
		result := f.request(ctx, http.MethodPost, entry.Server+acknowledgmentPath, entry.Credential.Reveal(), nil)
		if ctx.Err() != nil {
			return cancelAfterSaving(env, entry.Server)
		}
		if isTransientConfirmationFailure(result) {
			if attempt == 3 {
				fmt.Fprintln(output(env), "✗ Saved, but couldn't confirm with the server.")
				printFinishCommand(env, entry.Server)
				return 1
			}
			fmt.Fprintf(output(env), "  No answer, retrying (%d of 3)...\n", attempt+1)
			if !waitContext(ctx, f.pollInterval) {
				return cancelAfterSaving(env, entry.Server)
			}
			continue
		}
		if result.statusCode == http.StatusUnauthorized {
			var answer struct {
				Error string `json:"error"`
			}
			if result.err == nil && json.Unmarshal(result.body, &answer) == nil && answer.Error == "unknown_credential" {
				message := "✗ The server doesn't recognize this pairing. Run the same command again to start a new one."
				if earlier {
					message = "✗ The server doesn't recognize the earlier pairing. Run the same command again to start a new one."
				}
				return f.cleanupAndReport(ctx, env, entry, message)
			}
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server.")
			printFinishCommand(env, entry.Server)
			return 1
		}
		if result.statusCode != http.StatusOK || result.err != nil {
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server.")
			printFinishCommand(env, entry.Server)
			return 1
		}
		var answer struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(result.body, &answer); err != nil || answer.Result == "" {
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server.")
			printFinishCommand(env, entry.Server)
			return 1
		}
		switch answer.Result {
		case "ok":
			if ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			}
			record, exists, err := state.ReadState(env.StateDir)
			if err != nil || !exists {
				fmt.Fprintf(output(env), "✗ The server confirmed the pairing, but this computer couldn't record it: can't write %s.\n", state.StatePath(env.StateDir))
				printFinishCommand(env, entry.Server)
				return 1
			}
			record.Confirmed = true
			if ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			}
			if err := state.WriteState(env.StateDir, record); ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			} else if err != nil {
				fmt.Fprintf(output(env), "✗ The server confirmed the pairing, but this computer couldn't record it: can't write %s.\n", state.StatePath(env.StateDir))
				printFinishCommand(env, entry.Server)
				return 1
			}
			if ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			}
			fmt.Fprintln(output(env), "✓ Paired successfully.")
			return 0
		case "expired":
			message := "✗ The pairing didn't finish in time. Run the same command again to start a new one."
			if earlier {
				message = "✗ The earlier pairing didn't finish in time. Run the same command again to start a new one."
			}
			return f.cleanupAndReport(ctx, env, entry, message)
		case "failed":
			message := "✗ The pairing failed. Run the same command again to start a new one."
			if earlier {
				message = "✗ The earlier pairing failed. Run the same command again to start a new one."
			}
			return f.cleanupAndReport(ctx, env, entry, message)
		default:
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server.")
			printFinishCommand(env, entry.Server)
			return 1
		}
	}
	return 1
}

func isTransientConfirmationFailure(result requestResult) bool {
	return result.err != nil || !result.receivedResponse || result.statusCode >= http.StatusInternalServerError
}

func (f *pairingFlow) cleanupAndReport(ctx context.Context, env cli.Env, entry credential.Entry, message string) int {
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	if env.Store == nil || env.Store.Delete() != nil {
		if ctx.Err() != nil {
			return cancelAfterSaving(env, entry.Server)
		}
		fmt.Fprintln(output(env), "✗ Can't delete the secret store entry.")
		fmt.Fprintln(output(env), "  Make sure you are logged in to a desktop session and the keyring is unlocked.")
		return 1
	}
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	if err := state.DeleteState(env.StateDir); ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	} else if err != nil {
		printDeleteStateFailure(env)
		return 1
	}
	fmt.Fprintln(output(env), message)
	return 1
}
