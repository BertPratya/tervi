package pair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
)

func reportOutage(env cli.Env, alreadyReported bool) bool {
	if !alreadyReported {
		fmt.Fprintln(output(env), "Connection lost, retrying...")
	}
	return true
}

func (f *pairingFlow) poll(ctx context.Context, env cli.Env, server string, pollingKey secret.Value, deadline, shownExpiry time.Time) int {
	outage := false
	firstPoll := true
	for {
		if ctx.Err() != nil {
			return cancelPairing(env)
		}
		if !firstPoll {
			if !f.waitForNextPoll(ctx, deadline) {
				if ctx.Err() != nil {
					return cancelPairing(env)
				}
				fmt.Fprintln(output(env), "✗ The link expired. Run the command again.")
				return 1
			}
		}
		firstPoll = false
		if !f.now().Before(deadline) {
			fmt.Fprintln(output(env), "✗ The link expired. Run the command again.")
			return 1
		}

		result := f.requestUntil(ctx, http.MethodGet, server+pollPath, pollingKey.Reveal(), nil, deadline)
		if ctx.Err() != nil {
			return cancelPairing(env)
		}
		if result.receivedResponse && result.statusCode >= 500 {
			outage = reportOutage(env, outage)
			continue
		}
		if result.receivedResponse && result.statusCode == http.StatusUnauthorized {
			var errorResponse struct {
				Error string `json:"error"`
			}
			if result.err == nil && json.Unmarshal(result.body, &errorResponse) == nil && errorResponse.Error == "unknown_key" {
				if outage {
					fmt.Fprintln(output(env), "Connection restored.")
				}
				fmt.Fprintln(output(env), "✗ The server no longer knows this pairing. Run the command again.")
				return 1
			}
			if outage {
				fmt.Fprintln(output(env), "Connection restored.")
			}
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
			return 1
		}
		if result.receivedResponse && result.statusCode != http.StatusOK {
			if outage {
				fmt.Fprintln(output(env), "Connection restored.")
			}
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
			return 1
		}
		if result.err != nil || !result.receivedResponse {
			outage = reportOutage(env, outage)
			continue
		}
		if !json.Valid(result.body) {
			outage = reportOutage(env, outage)
			continue
		}
		poll, err := decodePollResponse(result.body)
		if err != nil {
			if outage {
				fmt.Fprintln(output(env), "Connection restored.")
			}
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
			return 1
		}
		if outage {
			fmt.Fprintln(output(env), "Connection restored.")
			outage = false
		}
		switch poll.Status {
		case "waiting_for_approval":
			continue
		case "rejected":
			fmt.Fprintln(output(env), "✗ Pairing was rejected in the browser.")
			return 1
		case "expired":
			fmt.Fprintln(output(env), "✗ The link expired. Run the command again.")
			return 1
		case "waiting_for_code":
			fmt.Fprintln(output(env), "✓ Approved in the browser.")
			if ctx.Err() != nil {
				return cancelPairing(env)
			}
			deadline = f.now().Add(time.Duration(poll.ExpiresInSeconds) * time.Second)
			return f.codePhase(ctx, env, server, pollingKey, poll.TriesLeft, deadline, shownExpiry)
		default:
			fmt.Fprintln(output(env), "✗ Unexpected answer from the server. Run the command again.")
			return 1
		}
	}
}

func (f *pairingFlow) waitForNextPoll(ctx context.Context, deadline time.Time) bool {
	remaining := deadline.Sub(f.now())
	if remaining <= 0 {
		return false
	}
	wait := f.pollInterval
	if remaining < wait {
		wait = remaining
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return f.now().Before(deadline)
	}
}

type pollResponse struct {
	Status           string `json:"status"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	TriesLeft        int    `json:"tries_left"`
	triesPresent     bool
}

func decodePollResponse(body []byte) (pollResponse, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return pollResponse{}, err
	}
	if fields == nil {
		return pollResponse{}, errors.New("poll response is not an object")
	}
	var response pollResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return pollResponse{}, err
	}
	var expires int
	if raw, ok := fields["expires_in_seconds"]; !ok || json.Unmarshal(raw, &expires) != nil || expires < 0 {
		return pollResponse{}, errors.New("invalid expiry in poll response")
	}
	response.ExpiresInSeconds = expires
	if raw, ok := fields["tries_left"]; ok {
		if json.Unmarshal(raw, &response.TriesLeft) != nil || response.TriesLeft < 0 {
			return pollResponse{}, errors.New("invalid tries_left in poll response")
		}
		response.triesPresent = true
	}
	if response.Status == "" || (response.Status == "waiting_for_code" && !response.triesPresent) {
		return pollResponse{}, errors.New("incomplete poll response")
	}
	return response, nil
}
