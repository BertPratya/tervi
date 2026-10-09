// Package flow implements the worker's network pairing flow.
package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/local"
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

	codePhase func(ctx context.Context, env local.Env, server string, pollingKey secret.Value, triesLeft int, deadline, shownExpiry time.Time) int
}

// New creates the worker pairing flow.
func New(opts Options) local.Flow { return newFlow(opts) }

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

func (f *pairingFlow) StartNew(ctx context.Context, env local.Env, server string) int {
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

func (f *pairingFlow) FinishEarlier(ctx context.Context, env local.Env, entry local.Entry) int {
	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return cancelAfterSaving(env, entry.Server)
	}
	fmt.Fprintf(output(env), "Finishing the earlier pairing with %s...\n", entry.Server)
	return f.confirm(ctx, env, entry, true)
}

func (f *pairingFlow) printStartScreen(env local.Env, server string, details computerInfo, approvalURL string, shownExpiry time.Time) {
	fmt.Fprintf(output(env), "Pairing this computer with %s\n", server)
	fmt.Fprintf(output(env), "  Computer: %s\n", displayOrUnknown(details.hostname))
	fmt.Fprintf(output(env), "  OS:       %s\n\n", displayOS(details.osName, details.osVersion))
	fmt.Fprintln(output(env), "Open this link (on any device) and approve this computer:")
	fmt.Fprintf(output(env), "  %s\n\n", approvalURL)
	fmt.Fprintf(output(env), "Waiting for approval... (expires at %s)\n", shownExpiry.Local().Format("15:04"))
}

func (f *pairingFlow) poll(ctx context.Context, env local.Env, server string, pollingKey secret.Value, deadline, shownExpiry time.Time) int {
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

func reportOutage(env local.Env, alreadyReported bool) bool {
	if !alreadyReported {
		fmt.Fprintln(output(env), "Connection lost, retrying...")
	}
	return true
}

func cancelPairing(env local.Env) int {
	fmt.Fprintln(output(env), "Pairing cancelled. Nothing was saved.")
	return 130
}

func output(env local.Env) io.Writer {
	if env.Stdout == nil {
		return io.Discard
	}
	return env.Stdout
}

type requestResult struct {
	statusCode       int
	body             []byte
	wroteRequest     bool
	receivedResponse bool
	err              error
}

func (f *pairingFlow) request(ctx context.Context, method, url, bearer string, body []byte) requestResult {
	return f.requestWithTimeout(ctx, method, url, bearer, body, f.requestTimeout)
}

func (f *pairingFlow) requestUntil(ctx context.Context, method, url, bearer string, body []byte, deadline time.Time) requestResult {
	timeout := deadline.Sub(f.now())
	if timeout > f.requestTimeout {
		timeout = f.requestTimeout
	}
	return f.requestWithTimeout(ctx, method, url, bearer, body, timeout)
}

func (f *pairingFlow) requestWithTimeout(ctx context.Context, method, url, bearer string, body []byte, timeout time.Duration) requestResult {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, url, bytesReader(body))
	if err != nil {
		return requestResult{err: err}
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			wrote.Store(true)
		}
	}}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := f.client.Do(request)
	if err != nil {
		return requestResult{wroteRequest: wrote.Load(), err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err == nil && len(data) > maxResponseSize {
		err = errors.New("response too large")
	}
	return requestResult{statusCode: response.StatusCode, body: data, wroteRequest: wrote.Load(), receivedResponse: true, err: err}
}

func bytesReader(body []byte) io.Reader {
	if body == nil {
		return nil
	}
	return strings.NewReader(string(body))
}

type startResponse struct {
	PollingKey       string `json:"polling_key"`
	ApprovalURL      string `json:"approval_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func decodeStartResponse(body []byte) (startResponse, error) {
	var response startResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return startResponse{}, err
	}
	if response.PollingKey == "" || response.ApprovalURL == "" || response.ExpiresInSeconds <= 0 {
		return startResponse{}, errors.New("incomplete start response")
	}
	return response, nil
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

type computerInfo struct {
	hostname  string
	osName    string
	osVersion string
	request   map[string]string
}

var (
	readHostname  = os.Hostname
	readOSRelease = func() ([]byte, error) { return os.ReadFile("/etc/os-release") }
)

func computerDetails() computerInfo {
	hostname, err := readHostname()
	if err != nil {
		hostname = ""
	}
	hostname = truncate(hostname, 64)

	osName, osVersion := "", ""
	if data, err := readOSRelease(); err == nil {
		values := parseOSRelease(data)
		osName = values["NAME"]
		osVersion = values["VERSION_ID"]
	}
	osName = truncate(osName, 64)
	osVersion = truncate(osVersion, 32)

	request := make(map[string]string, 3)
	if hostname != "" {
		request["hostname"] = hostname
	}
	if osName != "" {
		request["os_name"] = osName
	}
	if osVersion != "" {
		request["os_version"] = osVersion
	}
	return computerInfo{hostname: hostname, osName: osName, osVersion: osVersion, request: request}
}

func parseOSRelease(data []byte) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else {
				value = value[1 : len(value)-1]
			}
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		values[name] = value
	}
	return values
}

func truncate(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func displayOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func displayOS(name, version string) string {
	if name == "" && version == "" {
		return "unknown"
	}
	return displayOrUnknown(name) + " " + displayOrUnknown(version)
}
