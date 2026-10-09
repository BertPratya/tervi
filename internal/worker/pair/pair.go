// Package pair implements the worker's network pairing flow.
package pair

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultPollInterval   = 2 * time.Second
	startPath             = "/api/v1/pairings"
	pollPath              = "/api/v1/pairings/current"
	codePath              = "/api/v1/pairings/current/code"
	acknowledgmentPath    = "/api/v1/machines/current/acknowledgment"
	saveFailurePath       = "/api/v1/machines/current/save-failure"
	maxResponseSize       = 1 << 20
)

// Options configures request and polling timings. Non-positive durations and a
// nil clock use the production defaults.
type Options struct {
	RequestTimeout time.Duration
	PollInterval   time.Duration
	Now            func() time.Time
}

type pairingFlow struct {
	requestTimeout time.Duration
	pollInterval   time.Duration
	now            func() time.Time
	client         *http.Client

	codePhase func(ctx context.Context, env cli.Env, server string, pollingKey secret.Value, triesLeft int, deadline, shownExpiry time.Time) int
}

// New creates the worker pairing flow.
func New(opts Options) cli.Flow { return newFlow(opts) }

func newFlow(opts Options) *pairingFlow {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = defaultRequestTimeout
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	f := &pairingFlow{
		requestTimeout: opts.RequestTimeout,
		pollInterval:   opts.PollInterval,
		now:            opts.Now,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	f.codePhase = f.runCodePhase
	return f
}

func (f *pairingFlow) StartNew(ctx context.Context, env cli.Env, server string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return cancelPairing(env)
	}

	details := computerDetails()
	body, err := json.Marshal(details.request)
	if err != nil {
		fmt.Fprintln(output(env), "✗ The pairing request could not be prepared. Run the command again.")
		return 1
	}
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	result := f.request(ctx, http.MethodPost, server+startPath, "", body)
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	if result.receivedResponse && result.statusCode != http.StatusCreated {
		fmt.Fprintf(output(env), "✗ The server refused to start a pairing (HTTP %d).\n", result.statusCode)
		return 1
	}
	if result.err != nil {
		if result.receivedResponse {
			fmt.Fprintln(output(env), "✗ The server sent an answer that could not be read. Run the command again.")
			return 1
		}
		if result.wroteRequest {
			fmt.Fprintln(output(env), "✗ The server didn't answer, so the pairing could not be started. Run the command again.")
		} else {
			fmt.Fprintf(output(env), "✗ Can't reach %s. Check that the server is running, then run the command again.\n", server)
		}
		return 1
	}
	if result.statusCode != http.StatusCreated {
		fmt.Fprintf(output(env), "✗ The server refused to start a pairing (HTTP %d).\n", result.statusCode)
		return 1
	}
	start, err := decodeStartResponse(result.body)
	if err != nil {
		fmt.Fprintln(output(env), "✗ The server sent an answer that could not be read. Run the command again.")
		return 1
	}
	if ctx.Err() != nil {
		return cancelPairing(env)
	}
	startedAt := f.now()
	shownExpiry := startedAt.Add(time.Duration(start.ExpiresInSeconds) * time.Second)
	deadline := shownExpiry
	pollingKey := secret.FromString(start.PollingKey)
	f.printStartScreen(env, server, details, start.ApprovalURL, shownExpiry)
	return f.poll(ctx, env, server, pollingKey, deadline, shownExpiry)
}

func (f *pairingFlow) FinishEarlier(ctx context.Context, env cli.Env, entry credential.Entry) int {
	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	fmt.Fprintf(output(env), "Finishing the earlier pairing with %s...\n", entry.Server)
	return f.confirm(ctx, env, entry, true)
}

func (f *pairingFlow) printStartScreen(env cli.Env, server string, details computerInfo, approvalURL string, shownExpiry time.Time) {
	fmt.Fprintf(output(env), "Pairing this computer with %s\n", server)
	fmt.Fprintf(output(env), "  Computer: %s\n", displayOrUnknown(details.hostname))
	fmt.Fprintf(output(env), "  OS:       %s\n\n", displayOS(details.osName, details.osVersion))
	fmt.Fprintln(output(env), "Open this link (on any device) and approve this computer:")
	fmt.Fprintf(output(env), "  %s\n\n", approvalURL)
	fmt.Fprintf(output(env), "Waiting for approval... (expires at %s)\n", shownExpiry.Local().Format("15:04"))
}

func cancelPairing(env cli.Env) int {
	fmt.Fprintln(output(env), "Pairing cancelled. Nothing was saved.")
	return 130
}

func output(env cli.Env) io.Writer {
	if env.Stdout == nil {
		return io.Discard
	}
	return env.Stdout
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

func cancelAfterSaving(env cli.Env, server string) int {
	fmt.Fprintln(output(env), "Pairing cancelled before it was confirmed.")
	printFinishCommand(env, server)
	return 130
}

func printFinishCommand(env cli.Env, server string) {
	fmt.Fprintf(output(env), "  To finish, run:  tervi pair --server %s\n", server)
}

func printDeleteStateFailure(env cli.Env) {
	fmt.Fprintf(output(env), "✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", state.StatePath(env.StateDir), env.StateDir)
}
