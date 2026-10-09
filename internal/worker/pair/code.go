package pair

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
	"github.com/bertpratya/tervi/internal/worker/cli"
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

func (f *pairingFlow) runCodePhase(ctx context.Context, env cli.Env, server string, pollingKey secret.Value, triesLeft int, deadline, shownExpiry time.Time) int {
	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	fmt.Fprintf(output(env), "Type the code shown in the browser (expires at %s, %d tries left):\n", shownExpiry.Local().Format("15:04"), triesLeft)
	fmt.Fprint(output(env), "Code: ")

	lines := make(chan inputLine, 1)
	go readInputLines(ctx, env.Stdin, lines)
	inputEnded := false
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
			if line.err != nil {
				if errors.Is(line.err, io.EOF) {
					if line.value == "" {
						fmt.Fprintln(output(env), "\n✗ Input ended before a code was entered. Nothing was saved. Run the command again.")
						return 1
					}
					inputEnded = true
				} else {
					fmt.Fprintln(output(env), "\n✗ Couldn't read the code. Nothing was saved. Run the command again.")
					return 1
				}
			}
			code := strings.TrimSuffix(strings.TrimSuffix(line.value, "\n"), "\r")
			if code == "" {
				fmt.Fprint(output(env), "Code: ")
				if inputEnded {
					fmt.Fprintln(output(env), "\n✗ Input ended before a code was entered. Nothing was saved. Run the command again.")
					return 1
				}
				continue
			}
			body, _ := json.Marshal(map[string]string{"code": code})
			result := f.request(ctx, http.MethodPost, server+codePath, pollingKey.Reveal(), body)
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
				if inputEnded {
					fmt.Fprintln(output(env), "\n✗ Input ended before a code was entered. Nothing was saved. Run the command again.")
					return 1
				}
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
