package pair

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/credential"
)

const (
	testPollingKey  = "polling-key-must-not-be-printed"
	testApprovalKey = "approval-key-is-public-in-the-link"
)

func TestStartScreen(t *testing.T) {
	withComputerReaders(t, func() (string, error) { return "bert-desktop", nil }, func() ([]byte, error) {
		return []byte("NAME=Ubuntu\nVERSION_ID=26.04\n"), nil
	})

	now := time.Date(2026, time.January, 2, 14, 22, 0, 0, time.Local)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pairings":
			if r.Method != http.MethodPost {
				t.Errorf("start method = %s, want POST", r.Method)
			}
			var got map[string]string
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode start request: %v", err)
			}
			if got["hostname"] != "bert-desktop" || got["os_name"] != "Ubuntu" || got["os_version"] != "26.04" {
				t.Errorf("start details = %#v", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"polling_key":%q,"approval_url":%q,"expires_in_seconds":600}`, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey)
		case "/api/v1/pairings/current":
			writePoll(w, "rejected", 500, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, Now: func() time.Time { return now }}).
		StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	want := fmt.Sprintf("Pairing this computer with %s\n  Computer: bert-desktop\n  OS:       Ubuntu 26.04\n\nOpen this link (on any device) and approve this computer:\n  %s/pair/%s\n\nWaiting for approval... (expires at %s)\n✗ Pairing was rejected in the browser.\n", server.URL, server.URL, testApprovalKey, now.Add(10*time.Minute).Local().Format("15:04"))
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", safeOutput(got), want)
	}
}

func TestUnknownAndTruncatedDetails(t *testing.T) {
	t.Run("unreadable values are omitted and displayed as unknown", func(t *testing.T) {
		withComputerReaders(t, func() (string, error) { return "", errors.New("hostname unavailable") }, func() ([]byte, error) {
			return nil, errors.New("os-release unavailable")
		})
		var gotBody map[string]string
		server := startThenRejectServer(t, func(r *http.Request) {
			if r.URL.Path == "/api/v1/pairings" {
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Errorf("decode start request: %v", err)
				}
			}
		})
		defer server.Close()
		var stdout bytes.Buffer
		code := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
		if code != 1 {
			t.Fatalf("StartNew() = %d, want 1", code)
		}
		if len(gotBody) != 0 {
			t.Errorf("unreadable start details = %#v, want omitted", gotBody)
		}
		if !strings.Contains(stdout.String(), "  Computer: unknown\n  OS:       unknown\n") {
			t.Errorf("unknown details were not displayed: %q", safeOutput(stdout.String()))
		}
	})

	t.Run("hostname is truncated by runes", func(t *testing.T) {
		longHostname := strings.Repeat("é", 65)
		wantHostname := strings.Repeat("é", 63) + "…"
		withComputerReaders(t, func() (string, error) { return longHostname, nil }, func() ([]byte, error) {
			return []byte("NAME=TestOS\nVERSION_ID=1\n"), nil
		})
		var sent string
		server := startThenRejectServer(t, func(r *http.Request) {
			if r.URL.Path == "/api/v1/pairings" {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode start request: %v", err)
				}
				sent = body["hostname"]
			}
		})
		defer server.Close()
		var stdout bytes.Buffer
		code := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
		if code != 1 {
			t.Fatalf("StartNew() = %d, want 1", code)
		}
		if sent != wantHostname {
			t.Errorf("sent hostname = %q, want %q", sent, wantHostname)
		}
		if !strings.Contains(stdout.String(), "  Computer: "+wantHostname+"\n") {
			t.Errorf("truncated hostname not shown: %q", safeOutput(stdout.String()))
		}
	})
}

func TestCannotReach(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := "http://" + listener.Addr().String()
	_ = listener.Close()

	var stdout bytes.Buffer
	env := testEnv(t, &stdout)
	backend := credential.NewMemoryBackend()
	env.Store = credential.NewStore(backend)
	if code := newFlow(Options{RequestTimeout: 100 * time.Millisecond}).StartNew(context.Background(), env, server); code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	want := fmt.Sprintf("✗ Can't reach %s. Check that the server is running, then run the command again.\n", server)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(env.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state directory exists after failed start: %v", err)
	}
	if _, exists := backend.Value("worker"); exists {
		t.Error("secret store entry exists after failed start")
	}
}

func TestStartNoAnswerNotRetried(t *testing.T) {
	var starts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pairings" {
			http.NotFound(w, r)
			return
		}
		starts.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: 30 * time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("start requests = %d, want 1", got)
	}
	want := "✗ The server didn't answer, so the pairing could not be started. Run the command again.\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRejected(t *testing.T) {
	const pollInterval = 240 * time.Millisecond
	firstWaiting := make(chan struct{}, 1)
	var polls atomic.Int32
	var rejected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
			return
		}
		if polls.Add(1) == 1 {
			writePoll(w, "waiting_for_approval", 500, nil)
			firstWaiting <- struct{}{}
			return
		}
		if rejected.Load() {
			writePoll(w, "rejected", 500, nil)
			return
		}
		writePoll(w, "waiting_for_approval", 500, nil)
	}))
	defer server.Close()
	var stdout bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- newFlow(Options{RequestTimeout: time.Second, PollInterval: pollInterval}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	}()
	select {
	case <-firstWaiting:
	case <-time.After(time.Second):
		t.Fatal("first poll did not answer waiting_for_approval")
	}
	// Change the server state partway through the interval, away from a poll.
	time.Sleep(pollInterval / 3)
	rejectedAt := time.Now()
	rejected.Store(true)
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("StartNew() = %d, want 1", code)
		}
	case <-time.After(pollInterval + 100*time.Millisecond):
		t.Fatal("rejection was not reported within one poll interval plus margin")
	}
	if elapsed := time.Since(rejectedAt); elapsed > pollInterval+100*time.Millisecond {
		t.Errorf("rejection reported %s after the server switched to rejected, want within %s", elapsed, pollInterval+100*time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "✗ Pairing was rejected in the browser.\n") {
		t.Errorf("rejection not reported: %q", safeOutput(stdout.String()))
	}
}

func TestConnectionLostAndRestored(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
			return
		}
		if r.URL.Path != "/api/v1/pairings/current" {
			http.NotFound(w, r)
			return
		}
		switch polls.Add(1) {
		case 1:
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		case 2:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack connection: %v", err)
				return
			}
			_ = conn.Close()
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "temporarily down")
		case 4:
			_, _ = io.WriteString(w, `{"status":`)
		case 5:
			writePoll(w, "waiting_for_approval", 500, nil)
		default:
			writePoll(w, "rejected", 400, nil)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: 100 * time.Millisecond, PollInterval: time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	got := safeOutput(stdout.String())
	if count := strings.Count(got, "Connection lost, retrying...\n"); count != 1 {
		t.Errorf("connection-lost messages = %d, want 1; output %q", count, got)
	}
	if count := strings.Count(got, "Connection restored.\n"); count != 1 {
		t.Errorf("connection-restored messages = %d, want 1; output %q", count, got)
	}
	if polls.Load() != 6 || !strings.Contains(got, "✗ Pairing was rejected in the browser.") {
		t.Errorf("polling did not continue after recovery: polls=%d, want 6 calls including no-answer poll; output=%q", polls.Load(), got)
	}
}

func TestDeadlineDuringOutage(t *testing.T) {
	base := time.Date(2026, time.January, 2, 14, 22, 0, 0, time.Local)
	var clockMu sync.Mutex
	clockNow := base
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clockNow
	}
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 1)
			return
		}
		polls.Add(1)
		clockMu.Lock()
		clockNow = base.Add(2 * time.Second)
		clockMu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, Now: now}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	if polls.Load() != 1 {
		t.Errorf("polls = %d, want one before deadline check", polls.Load())
	}
	if !strings.Contains(stdout.String(), "✗ The link expired. Run the command again.\n") {
		t.Errorf("deadline message missing: %q", safeOutput(stdout.String()))
	}
}

func TestPollTimeoutCappedAtDeadline(t *testing.T) {
	base := time.Date(2026, time.January, 2, 14, 22, 0, 0, time.Local)
	var clockMu sync.Mutex
	clockNow := base
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clockNow
	}
	pollDuration := make(chan time.Duration, 1)
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 1)
			return
		}
		polls.Add(1)
		clockMu.Lock()
		clockNow = base.Add(2 * time.Second)
		clockMu.Unlock()
		started := time.Now()
		<-r.Context().Done()
		pollDuration <- time.Since(started)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: 2 * time.Second, PollInterval: time.Millisecond, Now: now}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	if got := polls.Load(); got != 1 {
		t.Fatalf("poll requests = %d, want 1", got)
	}
	if elapsed := <-pollDuration; elapsed > 1500*time.Millisecond {
		t.Errorf("poll request lasted %s, want it capped by the 1-second pairing deadline", elapsed)
	}
	if !strings.Contains(stdout.String(), "✗ The link expired. Run the command again.\n") {
		t.Errorf("deadline message missing: %q", safeOutput(stdout.String()))
	}
}

func TestPollsNeverOverlap(t *testing.T) {
	const interval = 20 * time.Millisecond
	var active, maxActive, polls atomic.Int32
	var mu sync.Mutex
	var starts, ends []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
			return
		}
		idx := int(polls.Add(1))
		cur := active.Add(1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}
		started := time.Now()
		mu.Lock()
		starts = append(starts, started)
		mu.Unlock()
		time.Sleep(8 * time.Millisecond)
		mu.Lock()
		ends = append(ends, time.Now())
		mu.Unlock()
		active.Add(-1)
		if idx < 3 {
			writePoll(w, "waiting_for_approval", 500, nil)
		} else {
			writePoll(w, "rejected", 500, nil)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	code := newFlow(Options{RequestTimeout: time.Second, PollInterval: interval}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 1 {
		t.Fatalf("StartNew() = %d, want 1", code)
	}
	if got := maxActive.Load(); got != 1 {
		t.Errorf("maximum active polls = %d, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 || len(ends) != 3 {
		t.Fatalf("poll timestamps = %d starts, %d ends; want 3 each", len(starts), len(ends))
	}
	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(ends[i-1])
		if gap < interval {
			t.Errorf("poll %d began %s after previous ended, want at least %s", i+1, gap, interval)
		}
	}
}

func TestUnexpectedAnswers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "finishing", statusCode: http.StatusOK, body: `{"status":"finishing","expires_in_seconds":500}`, want: "✗ Unexpected answer from the server. Run the command again.\n"},
		{name: "paired", statusCode: http.StatusOK, body: `{"status":"paired","expires_in_seconds":500}`, want: "✗ Unexpected answer from the server. Run the command again.\n"},
		{name: "failed", statusCode: http.StatusOK, body: `{"status":"failed","expires_in_seconds":500}`, want: "✗ Unexpected answer from the server. Run the command again.\n"},
		{name: "404", statusCode: http.StatusNotFound, body: `{"error":"not_found"}`, want: "✗ Unexpected answer from the server. Run the command again.\n"},
		{name: "401 unknown key", statusCode: http.StatusUnauthorized, body: `{"error":"unknown_key"}`, want: "✗ The server no longer knows this pairing. Run the command again.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/pairings" {
					writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
					return
				}
				w.WriteHeader(tc.statusCode)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var stdout bytes.Buffer
			code := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
			if code != 1 {
				t.Fatalf("StartNew() = %d, want 1", code)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("output = %q, want it to contain %q", safeOutput(stdout.String()), tc.want)
			}
		})
	}
}

func TestApprovedHandsOver(t *testing.T) {
	base := time.Date(2026, time.January, 2, 14, 22, 0, 0, time.Local)
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
			return
		}
		if polls.Add(1) == 1 {
			writePoll(w, "waiting_for_approval", 500, nil)
			return
		}
		writePoll(w, "waiting_for_code", 240, intPointer(4))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	f := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, Now: func() time.Time { return base }})
	var received struct {
		server      string
		key         secret.Value
		tries       int
		deadline    time.Time
		shownExpiry time.Time
	}
	f.codePhase = func(_ context.Context, _ cli.Env, server string, key secret.Value, tries int, deadline, shownExpiry time.Time) int {
		received.server = server
		received.key = key
		received.tries = tries
		received.deadline = deadline
		received.shownExpiry = shownExpiry
		return 42
	}
	code := f.StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	if code != 42 {
		t.Fatalf("StartNew() = %d, want codePhase result 42", code)
	}
	if received.server != server.URL || received.key.Reveal() != testPollingKey || received.tries != 4 {
		t.Errorf("handover values: server=%q key_matches=%t tries=%d", received.server, received.key.Reveal() == testPollingKey, received.tries)
	}
	if !received.deadline.Equal(base.Add(240 * time.Second)) {
		t.Errorf("deadline = %v, want %v", received.deadline, base.Add(240*time.Second))
	}
	if !received.shownExpiry.Equal(base.Add(600 * time.Second)) {
		t.Errorf("shown expiry = %v, want unchanged %v", received.shownExpiry, base.Add(600*time.Second))
	}
	if !strings.Contains(stdout.String(), "✓ Approved in the browser.\n") || !strings.Contains(stdout.String(), "expires at "+base.Add(600*time.Second).Local().Format("15:04")) {
		t.Errorf("approval handover output = %q", safeOutput(stdout.String()))
	}
}

func TestCtrlCBeforeSaving(t *testing.T) {
	t.Run("while starting", func(t *testing.T) {
		requested := make(chan struct{}, 4)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			select {
			case requested <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-time.After(100 * time.Millisecond):
			}
		}))
		defer server.Close()
		runCancelledFlow(t, server, requested, &requests, true)
	})

	t.Run("while polling", func(t *testing.T) {
		requested := make(chan struct{}, 4)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			select {
			case requested <- struct{}{}:
			default:
			}
			if r.URL.Path == "/api/v1/pairings" {
				writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
				return
			}
			select {
			case <-r.Context().Done():
			case <-time.After(100 * time.Millisecond):
			}
		}))
		defer server.Close()
		runCancelledFlow(t, server, requested, &requests, false)
	})
}

func TestPollingKeyNotPrinted(t *testing.T) {
	server := startThenRejectServer(t, nil)
	defer server.Close()
	var stdout bytes.Buffer
	newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond}).StartNew(context.Background(), testEnv(t, &stdout), server.URL)
	got := stdout.String()
	if strings.Contains(got, testPollingKey) {
		t.Error("polling key appeared in output")
	}
	if count := strings.Count(got, testApprovalKey); count != 1 {
		t.Errorf("approval key appeared %d times, want only once in printed link; output %q", count, safeOutput(got))
	}
	if !strings.Contains(got, "  "+server.URL+"/pair/"+testApprovalKey+"\n") {
		t.Errorf("approval URL was not printed verbatim: %q", safeOutput(got))
	}
}

func runCancelledFlow(t *testing.T, server *httptest.Server, requested <-chan struct{}, requests *atomic.Int32, starting bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	env := testEnv(t, &stdout)
	backend := credential.NewMemoryBackend()
	env.Store = credential.NewStore(backend)
	done := make(chan int, 1)
	go func() {
		done <- newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond}).StartNew(ctx, env, server.URL)
	}()
	wantRequests := 1
	if !starting {
		wantRequests = 2 // the start request and the poll request
	}
	for i := 0; i < wantRequests; i++ {
		select {
		case <-requested:
		case <-time.After(time.Second):
			t.Fatalf("request %d of %d did not start", i+1, wantRequests)
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Errorf("StartNew() = %d, want 130", code)
		}
	case <-time.After(time.Second):
		t.Fatal("StartNew did not stop after cancellation")
	}
	got := stdout.String()
	if starting {
		if got != "Pairing cancelled. Nothing was saved.\n" {
			t.Errorf("stdout = %q, want cancellation message only", got)
		}
	} else if !strings.HasSuffix(got, "Pairing cancelled. Nothing was saved.\n") {
		t.Errorf("stdout = %q, want cancellation message after the start screen", safeOutput(got))
	}
	if _, err := os.Stat(env.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state directory exists after cancellation: %v", err)
	}
	if _, exists := backend.Value("worker"); exists {
		t.Error("secret store entry exists after cancellation")
	}
	if got := requests.Load(); got != int32(wantRequests) {
		t.Errorf("server received %d requests, want exactly %d", got, wantRequests)
	}
}

func testEnv(t *testing.T, stdout io.Writer) cli.Env {
	t.Helper()
	return cli.Env{
		Stdin:    strings.NewReader(""),
		Stdout:   stdout,
		Stderr:   io.Discard,
		Store:    credential.NewStore(credential.NewMemoryBackend()),
		StateDir: filepath.Join(t.TempDir(), "state"),
	}
}

func startThenRejectServer(t *testing.T, observe func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observe != nil {
			observe(r)
		}
		if r.URL.Path == "/api/v1/pairings" {
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
			return
		}
		writePoll(w, "rejected", 500, nil)
	}))
}

func writeStart(w http.ResponseWriter, pollingKey, approvalURL string, expires int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"polling_key": pollingKey, "approval_url": approvalURL, "expires_in_seconds": expires,
	})
}

func writePoll(w http.ResponseWriter, status string, expires int, triesLeft *int) {
	body := map[string]any{"status": status, "expires_in_seconds": expires}
	if triesLeft != nil {
		body["tries_left"] = *triesLeft
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func intPointer(value int) *int { return &value }

func serverURL(r *http.Request) string { return "http://" + r.Host }

func safeOutput(value string) string {
	for _, secretValue := range []string{testPollingKey, testCode, testCredential} {
		value = strings.ReplaceAll(value, secretValue, "[redacted]")
	}
	return value
}

func withComputerReaders(t *testing.T, hostname func() (string, error), osRelease func() ([]byte, error)) {
	t.Helper()
	oldHostname, oldOSRelease := readHostname, readOSRelease
	readHostname, readOSRelease = hostname, osRelease
	t.Cleanup(func() { readHostname, readOSRelease = oldHostname, oldOSRelease })
}
