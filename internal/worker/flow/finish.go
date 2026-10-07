package flow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/local"
)

type codeAnswer struct {
	Result       string `json:"result"`
	TriesLeft    int    `json:"tries_left"`
	triesPresent bool
	Status       string
	Credential   secret.Value
	MachineID    string `json:"machine_id"`
}

func (answer *codeAnswer) UnmarshalJSON(data []byte) error {
	var response struct {
		Result     string `json:"result"`
		TriesLeft  *int   `json:"tries_left"`
		Status     string `json:"status"`
		Credential string `json:"credential"`
		MachineID  string `json:"machine_id"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	answer.Result = response.Result
	answer.Status = response.Status
	answer.Credential = secret.FromString(response.Credential)
	answer.MachineID = response.MachineID
	if response.TriesLeft != nil {
		answer.TriesLeft = *response.TriesLeft
		answer.triesPresent = true
	}
	return nil
}

type inputLine struct {
	value string
	err   error
}

func (f *pairingFlow) runCodePhase(ctx context.Context, env local.Env, server string, pollingKey secret.Value, triesLeft int, deadline, shownExpiry time.Time) int {
	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	fmt.Fprintf(output(env), "Type the code shown in the browser (expires at %s, %d tries left):\n", shownExpiry.Local().Format("15:04"), triesLeft)
	fmt.Fprint(output(env), "Code: ")

	lines := make(chan inputLine, 1)
	go readInputLines(ctx, env.Stdin, lines)
	remaining := deadline.Sub(f.now())
	if remaining <= 0 {
		fmt.Fprintln(output(env), "\n✗ The code expired. Run the command again.")
		return 1
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return cancelPairing(env)
		case <-timer.C:
			fmt.Fprintln(output(env), "\n✗ The code expired. Run the command again.")
			return 1
		case line := <-lines:
			if ctx.Err() != nil {
				return cancelPairing(env)
			}
			select {
			case <-timer.C:
				fmt.Fprintln(output(env), "\n✗ The code expired. Run the command again.")
				return 1
			default:
			}
			if !f.now().Before(deadline) {
				fmt.Fprintln(output(env), "\n✗ The code expired. Run the command again.")
				return 1
			}
			if line.err != nil && line.value == "" {
				if errors.Is(line.err, io.EOF) {
					return cancelPairing(env)
				}
				fmt.Fprintln(output(env), "\n✗ Couldn't read the code. Pairing was not completed.")
				return 1
			}
			code := strings.TrimSuffix(strings.TrimSuffix(line.value, "\n"), "\r")
			if code == "" {
				fmt.Fprint(output(env), "Code: ")
				continue
			}
			body, _ := json.Marshal(map[string]string{"code": code})
			result := f.requestUntil(ctx, http.MethodPost, server+codePath, pollingKey.Reveal(), body, deadline)
			if ctx.Err() != nil {
				return cancelPairing(env)
			}
			if result.err != nil || !result.receivedResponse || result.statusCode >= http.StatusInternalServerError {
				fmt.Fprintln(output(env), "✗ No answer from the server after sending the code. Run the same command again to start over.")
				return 1
			}
			if result.statusCode == http.StatusUnauthorized {
				var answer struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(result.body, &answer) == nil && answer.Error == "unknown_key" {
					fmt.Fprintln(output(env), "✗ The server no longer knows this pairing. Run the command again.")
				} else {
					fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
				}
				return 1
			}
			if result.statusCode != http.StatusOK {
				fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
				return 1
			}
			if !json.Valid(result.body) {
				fmt.Fprintln(output(env), "✗ No answer from the server after sending the code. Run the same command again to start over.")
				return 1
			}
			var answer codeAnswer
			if err := json.Unmarshal(result.body, &answer); err != nil || answer.Result == "" {
				fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
				return 1
			}
			switch answer.Result {
			case "wrong_code":
				if !answer.triesPresent || answer.TriesLeft < 0 {
					fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
					return 1
				}
				fmt.Fprintf(output(env), "✗ Wrong code. %d tries left.\n", answer.TriesLeft)
				fmt.Fprint(output(env), "Code: ")
			case "failed":
				fmt.Fprintln(output(env), "✗ Too many wrong codes. Pairing failed. Run the command again.")
				return 1
			case "expired":
				fmt.Fprintln(output(env), "✗ The code expired. Run the command again.")
				return 1
			case "not_waiting_for_code":
				if answer.Status == "" {
					fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
					return 1
				}
				fmt.Fprintln(output(env), "✗ The pairing is no longer waiting for a code. Run the command again.")
				return 1
			case "accepted":
				if answer.Credential.Reveal() == "" || answer.MachineID == "" {
					fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
					return 1
				}
				fmt.Fprintln(output(env), "✓ Code accepted.")
				return f.saveAndConfirm(ctx, env, server, answer)
			default:
				fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
				return 1
			}
		}
	}
}

func readInputLines(ctx context.Context, input io.Reader, lines chan<- inputLine) {
	if input == nil {
		select {
		case lines <- inputLine{err: io.EOF}:
		case <-ctx.Done():
		}
		return
	}
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadString('\n')
		select {
		case lines <- inputLine{value: line, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (f *pairingFlow) saveAndConfirm(ctx context.Context, env local.Env, server string, answer codeAnswer) int {
	ctx = normalizeContext(ctx)
	entry := local.Entry{Server: server, Credential: answer.Credential}
	state := local.State{MachineID: answer.MachineID}
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	if err := local.WriteState(env.StateDir, state); ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	} else if err != nil {
		fmt.Fprintf(output(env), "✗ Can't write %s. Pairing was not completed.\n", local.StatePath(env.StateDir))
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
	state.CredentialSaved = true
	if ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	}
	if err := local.WriteState(env.StateDir, state); ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	} else if err != nil {
		fmt.Fprintf(output(env), "✗ Can't write %s.\n", local.StatePath(env.StateDir))
		printFinishCommand(env, server)
		return 1
	}
	fmt.Fprintln(output(env), "✓ Credential saved.")
	if ctx.Err() != nil {
		return cancelAfterSaving(env, server)
	}
	return f.confirm(ctx, env, entry, false)
}

func (f *pairingFlow) saveFailed(ctx context.Context, env local.Env, entry local.Entry) int {
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
	if err := local.DeleteState(env.StateDir); ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	} else if err != nil {
		printDeleteStateFailure(env)
		return 1
	}
	return 1
}

func (f *pairingFlow) reportSaveFailure(ctx context.Context, server string, credential secret.Value) {
	if ctx.Err() != nil {
		return
	}
	_ = f.request(ctx, http.MethodPost, server+saveFailurePath, credential.Reveal(), nil)
}

func (f *pairingFlow) confirm(ctx context.Context, env local.Env, entry local.Entry, earlier bool) int {
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
			state, exists, err := local.ReadState(env.StateDir)
			if err != nil || !exists {
				fmt.Fprintf(output(env), "✗ The server confirmed the pairing, but this computer couldn't record it: can't write %s.\n", local.StatePath(env.StateDir))
				printFinishCommand(env, entry.Server)
				return 1
			}
			state.Confirmed = true
			if ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			}
			if err := local.WriteState(env.StateDir, state); ctx.Err() != nil {
				return cancelAfterSaving(env, entry.Server)
			} else if err != nil {
				fmt.Fprintf(output(env), "✗ The server confirmed the pairing, but this computer couldn't record it: can't write %s.\n", local.StatePath(env.StateDir))
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

func (f *pairingFlow) cleanupAndReport(ctx context.Context, env local.Env, entry local.Entry, message string) int {
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
	if err := local.DeleteState(env.StateDir); ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	} else if err != nil {
		printDeleteStateFailure(env)
		return 1
	}
	fmt.Fprintln(output(env), message)
	return 1
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func cancelAfterSaving(env local.Env, server string) int {
	fmt.Fprintln(output(env), "Pairing cancelled before it was confirmed.")
	printFinishCommand(env, server)
	return 130
}

func printFinishCommand(env local.Env, server string) {
	fmt.Fprintf(output(env), "  To finish, run:  tervi pair --server %s\n", server)
}

func printDeleteStateFailure(env local.Env) {
	fmt.Fprintf(output(env), "✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", local.StatePath(env.StateDir), env.StateDir)
}
