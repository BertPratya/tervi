package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/service"
	"github.com/bertpratya/tervi/internal/worker/state"
)

type commandRunner struct {
	calls       [][]string
	streams     [][]string
	output      string
	chunks      []string
	runOutput   string
	runError    error
	streamError error
	streamCheck func()
	runFunc     func(context.Context, string, ...string) (string, error)
}

func (r *commandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.runFunc != nil {
		return r.runFunc(ctx, name, args...)
	}
	return r.runOutput, r.runError
}

func (r *commandRunner) Stream(_ context.Context, out io.Writer, name string, args ...string) error {
	r.streams = append(r.streams, append([]string{name}, args...))
	if len(r.chunks) > 0 {
		for _, chunk := range r.chunks {
			_, _ = io.WriteString(out, chunk)
			if r.streamCheck != nil {
				r.streamCheck()
			}
		}
	} else {
		_, _ = io.WriteString(out, r.output)
		if r.streamCheck != nil {
			r.streamCheck()
		}
	}
	return r.streamError
}

type statusResult struct {
	answer control.StatusAnswer
	err    error
}

type commandControl struct {
	statuses     []statusResult
	statusCalls  int
	statusFunc   func(context.Context) (control.StatusAnswer, error)
	connect      control.ConnectAnswer
	connectErr   error
	connectCalls int
	connectFunc  func(context.Context) (control.ConnectAnswer, error)
}

func (c *commandControl) Status(ctx context.Context) (control.StatusAnswer, error) {
	if c.statusFunc != nil {
		c.statusCalls++
		return c.statusFunc(ctx)
	}
	i := c.statusCalls
	c.statusCalls++
	if i < len(c.statuses) {
		return c.statuses[i].answer, c.statuses[i].err
	}
	return control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}, nil
}

func (c *commandControl) Connect(ctx context.Context) (control.ConnectAnswer, error) {
	c.connectCalls++
	if c.connectFunc != nil {
		return c.connectFunc(ctx)
	}
	return c.connect, c.connectErr
}

func commandEnv(t *testing.T, runner *commandRunner, client *commandControl, paired bool) (Env, *credential.MemoryBackend, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	stateDir := filepath.Join(t.TempDir(), "state")
	backend := credential.NewMemoryBackend()
	if paired {
		if err := state.WriteState(stateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		if err := backend.Set("worker", `{"server":"http://localhost:8080","credential":"`+testCredential+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdout: &stdout, Stderr: &stderr, Store: credential.NewStore(backend), StateDir: stateDir,
		Runner: runner, ProgramPath: "/usr/bin/true", ServiceDir: t.TempDir(), ControlClient: client,
	}
	return env, backend, &stdout, &stderr
}

func TestInstalledCheck(t *testing.T) {
	tempRoot := t.TempDir()
	tempProgram := filepath.Join(tempRoot, "tervi")
	if err := os.WriteFile(tempProgram, []byte("worker"), 0600); err != nil {
		t.Fatal(err)
	}
	otherRoot := t.TempDir()
	goBuildProgram := filepath.Join(otherRoot, "go-build", "b001", "tervi")
	if err := os.MkdirAll(filepath.Dir(goBuildProgram), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goBuildProgram, []byte("worker"), 0600); err != nil {
		t.Fatal(err)
	}
	linkedTempRoot := filepath.Join(t.TempDir(), "tmp-link")
	if err := os.Symlink(tempRoot, linkedTempRoot); err != nil {
		t.Fatal(err)
	}
	installedProgram := "/usr/bin/true"
	cases := []struct {
		name    string
		program string
		tempDir string
		blocked bool
	}{
		{name: "inside temporary folder", program: tempProgram, tempDir: tempRoot, blocked: true},
		{name: "temporary folder reached through symlink", program: tempProgram, tempDir: linkedTempRoot, blocked: true},
		{name: "go build folder elsewhere", program: goBuildProgram, tempDir: t.TempDir(), blocked: true},
		{name: "installed", program: installedProgram, tempDir: t.TempDir(), blocked: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.IsTemporaryProgram(tc.program, tc.tempDir); got != tc.blocked {
				t.Fatalf("temporary check for %q did not match blocked=%v", tc.program, tc.blocked)
			}
			runner := &commandRunner{}
			client := &commandControl{statuses: []statusResult{{answer: control.StatusAnswer{State: "connected", Server: "http://localhost:8080", Since: time.Now().Format(time.RFC3339)}}}, connect: control.ConnectAnswer{Result: "already_connected"}}
			env, _, stdout, stderr := commandEnv(t, runner, client, true)
			env.ProgramPath = tc.program
			if !tc.blocked {
				writeCurrentServiceFile(t, env)
			}
			code := Run(context.Background(), []string{"connect"}, env, nil)
			if tc.blocked {
				want := "tervi must be installed to run in the background. Install it with: go install ./cmd/tervi\n"
				if code != 1 || stdout.String() != want || stderr.Len() != 0 {
					t.Errorf("Run(connect) = %d, stdout=%q stderr=%q; want install message", code, stdout.String(), stderr.String())
				}
				if len(runner.calls) != 0 || client.statusCalls != 0 {
					t.Errorf("temporary executable did work: runner=%v status calls=%d", runner.calls, client.statusCalls)
				}
			} else if code != 0 || stdout.String() != "Already connected.\n" || stderr.Len() != 0 || client.connectCalls != 1 {
				t.Errorf("installed Run(connect) = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestConnectControlError(t *testing.T) {
	runner := &commandRunner{}
	client := &commandControl{
		statuses:   []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}},
		connectErr: errors.New("daemon control request failed"),
	}
	env, _, stdout, stderr := commandEnv(t, runner, client, true)
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 1 {
		t.Fatalf("Run(connect) = %d, want 1", code)
	}
	want := "✗ Can't reach tervi in the background: daemon control request failed\nCheck with: tervi status\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want %q and empty", stdout.String(), stderr.String(), want)
	}
}

func TestConnectCtrlC(t *testing.T) {
	runner := &commandRunner{}
	started := make(chan struct{})
	client := &commandControl{
		connectFunc: func(ctx context.Context) (control.ConnectAnswer, error) {
			close(started)
			<-ctx.Done()
			return control.ConnectAnswer{}, ctx.Err()
		},
	}
	env, _, stdout, stderr := commandEnv(t, runner, client, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"connect"}, env, nil) }()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Connect was not called")
	}
	callsAtCancel := len(runner.calls)
	select {
	case code := <-done:
		if code != 130 {
			t.Errorf("Run(connect) = %d, want 130", code)
		}
	case <-time.After(time.Second):
		t.Fatal("connect did not return after cancellation")
	}
	if got, want := stdout.String(), "Connecting was cancelled; tervi keeps trying in the background.\n"; got != want {
		t.Errorf("stdout=%q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr=%q, want empty", stderr.String())
	}
	if len(runner.calls) != callsAtCancel {
		t.Errorf("Runner calls changed after cancel: before=%d after=%v", callsAtCancel, runner.calls)
	}
	assertNoServiceStopCalls(t, runner.calls)
}

func TestConnectAfterRejectedStatusStillCallsConnect(t *testing.T) {
	runner := &commandRunner{}
	client := &commandControl{
		statuses: []statusResult{{answer: control.StatusAnswer{State: "not_connecting", StopReason: ptrString("credential_rejected")}}},
		connect:  control.ConnectAnswer{Result: "connected"},
	}
	env, _, stdout, stderr := commandEnv(t, runner, client, true)
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 0 {
		t.Fatalf("Run(connect) = %d, want 0", code)
	}
	if stdout.String() != "✓ Connected.\n" || stderr.Len() != 0 || client.connectCalls != 1 {
		t.Errorf("stdout=%q stderr=%q Connect calls=%d, want connected output and one Connect call", stdout.String(), stderr.String(), client.connectCalls)
	}
}

func TestConnectCommandResults(t *testing.T) {
	lastError := "connection refused"
	for _, tc := range []struct {
		name   string
		answer control.ConnectAnswer
		err    error
		want   string
		code   int
	}{
		{name: "connected", answer: control.ConnectAnswer{Result: "connected"}, want: "✓ Connected.\n", code: 0},
		{name: "already connected", answer: control.ConnectAnswer{Result: "already_connected"}, want: "Already connected.\n", code: 0},
		{name: "still trying", answer: control.ConnectAnswer{Result: "still_trying", LastError: &lastError}, want: "Not connected yet: connection refused. tervi keeps trying in the background.\nCheck with: tervi status\n", code: 1},
		{name: "still trying without error", answer: control.ConnectAnswer{Result: "still_trying"}, want: "Not connected yet. tervi keeps trying in the background.\nCheck with: tervi status\n", code: 1},
		{name: "write failed", answer: control.ConnectAnswer{Result: "failed", Path: "/home/user/.config/tervi/state.json"}, want: "✗ Can't write /home/user/.config/tervi/state.json.\n", code: 1},
		{name: "rejected", answer: control.ConnectAnswer{Result: "rejected"}, want: "✗ The server doesn't accept this computer's credential.\nPair again with: tervi pair --server http://localhost:8080\n", code: 1},
		{name: "not paired", answer: control.ConnectAnswer{Result: "not_paired"}, want: "This computer isn't paired. Pair it with: tervi pair --server <url>\n", code: 1},
		{name: "control error", err: errors.New("socket unavailable"), want: "✗ Can't reach tervi in the background: socket unavailable\nCheck with: tervi status\n", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &commandRunner{}
			client := &commandControl{
				statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}},
				connect:  tc.answer, connectErr: tc.err,
			}
			env, _, stdout, stderr := commandEnv(t, runner, client, true)
			if tc.name == "already connected" {
				writeCurrentServiceFile(t, env)
			}
			if code := Run(context.Background(), []string{"connect"}, env, nil); code != tc.code {
				t.Errorf("Run(connect) = %d, want %d", code, tc.code)
			}
			if stdout.String() != tc.want || stderr.Len() != 0 || client.connectCalls != 1 {
				t.Errorf("stdout=%q stderr=%q, want %q and empty", stdout.String(), stderr.String(), tc.want)
			}
			assertNoCredential(t, stdout.String(), stderr.String())
		})
	}
}

func TestConnectFromDifferentInstallPrintsConnected(t *testing.T) {
	runner := &commandRunner{}
	client := &commandControl{
		statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}},
		connect:  control.ConnectAnswer{Result: "already_connected"},
	}
	env, _, stdout, stderr := commandEnv(t, runner, client, true)
	servicePath := filepath.Join(env.ServiceDir, "tervi.service")
	oldService := "[Unit]\nDescription=tervi worker\n\n[Service]\nExecStart=\"/usr/local/bin/tervi\" daemon\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
	if err := os.WriteFile(servicePath, []byte(oldService), 0600); err != nil {
		t.Fatal(err)
	}

	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 0 {
		t.Fatalf("Run(connect) = %d, want 0", code)
	}
	if stdout.String() != "✓ Connected.\n" || stderr.Len() != 0 || client.connectCalls != 1 {
		t.Errorf("stdout=%q stderr=%q Connect calls=%d, want connected output and one Connect call", stdout.String(), stderr.String(), client.connectCalls)
	}
	wantCalls := [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "tervi"}, {"systemctl", "--user", "restart", "tervi"}}
	if fmt.Sprint(runner.calls) != fmt.Sprint(wantCalls) {
		t.Errorf("systemctl calls = %v, want %v", runner.calls, wantCalls)
	}
}

func writeCurrentServiceFile(t *testing.T, env Env) {
	t.Helper()
	programPath, err := filepath.EvalSymlinks(env.ProgramPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := fmt.Sprintf("ExecStart=\"%s\" daemon\n", programPath)
	if err := os.WriteFile(filepath.Join(env.ServiceDir, "tervi.service"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConnectNotPaired(t *testing.T) {
	runner := &commandRunner{}
	client := &commandControl{}
	env, _, stdout, stderr := commandEnv(t, runner, client, false)
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 1 {
		t.Fatalf("Run(connect) = %d, want 1", code)
	}
	want := "This computer isn't paired. Pair it with: tervi pair --server <url>\n"
	if stdout.String() != want || stderr.Len() != 0 || len(runner.calls) != 0 {
		t.Errorf("stdout=%q stderr=%q systemctl=%v, want %q, empty, and no calls", stdout.String(), stderr.String(), runner.calls, want)
	}
}

func TestConnectStartsStoppedService(t *testing.T) {
	for _, tc := range []struct {
		name           string
		currentService bool
		statuses       []statusResult
		answer         string
		wantCalls      [][]string
	}{
		{
			name:     "missing service is set up and started",
			statuses: []statusResult{{err: control.ErrNotRunning}},
			answer:   "connected",
			wantCalls: [][]string{
				{"loginctl", "enable-linger"},
				{"systemctl", "--user", "daemon-reload"},
				{"systemctl", "--user", "enable", "tervi"},
				{"systemctl", "--user", "restart", "tervi"},
			},
		},
		{
			name:           "stopped current service is started",
			currentService: true,
			statuses: []statusResult{
				{err: control.ErrNotRunning},
				{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}},
			},
			answer:    "already_connected",
			wantCalls: [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "enable", "--now", "tervi"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &commandRunner{}
			client := &commandControl{
				statuses: tc.statuses,
				connect:  control.ConnectAnswer{Result: tc.answer},
			}
			env, _, stdout, stderr := commandEnv(t, runner, client, true)
			if tc.currentService {
				writeCurrentServiceFile(t, env)
			}
			if code := Run(context.Background(), []string{"connect"}, env, nil); code != 0 {
				t.Fatalf("Run(connect) = %d, want 0", code)
			}
			if stdout.String() != "✓ Connected.\n" || stderr.Len() != 0 || fmt.Sprint(runner.calls) != fmt.Sprint(tc.wantCalls) || client.connectCalls != 1 {
				t.Errorf("stdout=%q stderr=%q calls=%v connect calls=%d", stdout.String(), stderr.String(), runner.calls, client.connectCalls)
			}
		})
	}
}

func TestConnectServiceFails(t *testing.T) {
	runner := &commandRunner{runOutput: "system bus not available\n", runError: errors.New("exit status 1")}
	client := &commandControl{statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}}}
	env, backend, stdout, stderr := commandEnv(t, runner, client, true)
	stateBefore, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	entryBefore, _ := backend.Value("worker")
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 1 {
		t.Fatalf("Run(connect) = %d, want 1", code)
	}
	want := "✗ Can't start tervi in the background: system bus not available\nTry again with: tervi connect\n"
	if stdout.String() != want || stderr.Len() != 0 || client.connectCalls != 0 {
		t.Errorf("stdout=%q stderr=%q connect calls=%d", stdout.String(), stderr.String(), client.connectCalls)
	}
	stateAfter, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Errorf("state changed or became unreadable: err=%v before=%q after=%q", err, stateBefore, stateAfter)
	}
	entryAfter, _ := backend.Value("worker")
	if entryAfter != entryBefore {
		t.Error("credential file entry changed")
	}
}

func TestConnectReturnsAfterLastLine(t *testing.T) {
	runner := &commandRunner{}
	client := &commandControl{
		statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}},
		connect:  control.ConnectAnswer{Result: "connected"},
	}
	env, _, stdout, stderr := commandEnv(t, runner, client, true)
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 0 {
		t.Fatalf("Run(connect) = %d, want 0", code)
	}
	if stdout.String() != "✓ Connected.\n" || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	assertNoServiceStopCalls(t, runner.calls)
}

func assertNoServiceStopCalls(t *testing.T, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if len(call) < 3 || call[0] != "systemctl" || call[1] != "--user" {
			continue
		}
		if (call[2] == "stop" || call[2] == "kill" || call[2] == "disable") && (len(call) < 4 || call[3] == "tervi") {
			t.Errorf("command tried to stop the background service: %v", call)
		}
	}
}

func TestStatusOutputs(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-12 * time.Minute)
	lastError := "connection refused"
	cases := []struct {
		name     string
		paired   bool
		statuses []statusResult
		want     string
		code     int
	}{
		{name: "connected", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "connected", Server: "http://localhost:8080", Since: since.Format(time.RFC3339)}}}, want: fmt.Sprintf("● Connected to http://localhost:8080\n  Machine: %s\n  Since:   %s (%s)\n", hostname, since.Local().Format("15:04"), sinceText(12*time.Minute)), code: 0},
		{name: "reconnecting", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080", Try: 3, NextTryInSeconds: 4, LastError: &lastError}}}, want: "○ Reconnecting to http://localhost:8080\n  Try 3, next in 4 seconds\n  Last error: connection refused\n", code: 1},
		{name: "reconnecting uses paired server when daemon has none", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Try: 3, NextTryInSeconds: 4, LastError: &lastError}}}, want: "○ Reconnecting to http://localhost:8080\n  Try 3, next in 4 seconds\n  Last error: connection refused\n", code: 1},
		{name: "reconnecting in one second", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080", Try: 3, NextTryInSeconds: 1}}}, want: "○ Reconnecting to http://localhost:8080\n  Try 3, next in 1 second\n", code: 1},
		{name: "reconnecting in two seconds", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080", Try: 3, NextTryInSeconds: 2}}}, want: "○ Reconnecting to http://localhost:8080\n  Try 3, next in 2 seconds\n", code: 1},
		{name: "trying now without error", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080", Try: 2, NextTryInSeconds: 0}}}, want: "○ Reconnecting to http://localhost:8080\n  Try 2, trying now\n", code: 1},
		{name: "not running", paired: true, statuses: []statusResult{{err: control.ErrNotRunning}}, want: "✗ tervi isn't running in the background.\n  Start it with: tervi connect\n", code: 1},
		{name: "not paired", want: "✗ This computer isn't paired.\n  Pair it with: tervi pair --server <url>\n", code: 1},
		{name: "rejected", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "not_connecting", StopReason: ptrString("credential_rejected")}}}, want: "✗ Not connecting: the server doesn't accept this computer's credential.\n  Pair again with: tervi pair --server http://localhost:8080\n", code: 1},
		{name: "not connecting", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "not_connecting"}}}, want: "○ Not connecting.\n  Connect with: tervi connect\n", code: 1},
		{name: "daemon says not paired", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "not_paired"}}}, want: "✗ This computer isn't paired.\n  Pair it with: tervi pair --server <url>\n", code: 1},
		{name: "broken answer", paired: true, statuses: []statusResult{{err: errors.New("invalid daemon response")}}, want: "✗ Can't reach tervi in the background: invalid daemon response\n", code: 1},
		{name: "unknown state", paired: true, statuses: []statusResult{{answer: control.StatusAnswer{State: "sleeping"}}}, want: "✗ Can't reach tervi in the background: unexpected daemon status\n", code: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &commandRunner{}
			client := &commandControl{statuses: tc.statuses}
			env, _, stdout, stderr := commandEnv(t, runner, client, tc.paired)
			if code := Run(context.Background(), []string{"status"}, env, nil); code != tc.code {
				t.Errorf("Run(status) = %d, want %d", code, tc.code)
			}
			if stdout.String() != tc.want || stderr.Len() != 0 {
				t.Errorf("stdout=%q stderr=%q, want %q and empty", stdout.String(), stderr.String(), tc.want)
			}
		})
	}
	t.Run("cancelled while waiting", func(t *testing.T) {
		runner := &commandRunner{}
		started := make(chan struct{})
		client := &commandControl{statusFunc: func(ctx context.Context) (control.StatusAnswer, error) {
			close(started)
			<-ctx.Done()
			return control.StatusAnswer{}, ctx.Err()
		}}
		env, _, stdout, stderr := commandEnv(t, runner, client, true)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() { done <- Run(ctx, []string{"status"}, env, nil) }()
		select {
		case <-started:
			cancel()
		case <-time.After(time.Second):
			cancel()
			t.Fatal("Status was not called")
		}
		select {
		case code := <-done:
			if code != 130 {
				t.Errorf("Run(status) = %d, want 130", code)
			}
		case <-time.After(time.Second):
			t.Fatal("status did not return after cancellation")
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("cancelled status output = (%q, %q), want empty", stdout.String(), stderr.String())
		}
	})
	// Pairing state failures keep the original slice-1 wording.
	t.Run("state unreadable", func(t *testing.T) {
		runner := &commandRunner{}
		client := &commandControl{}
		env, _, stdout, stderr := commandEnv(t, runner, client, false)
		if err := os.MkdirAll(env.StateDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(state.StatePath(env.StateDir), 0700); err != nil {
			t.Fatal(err)
		}
		if code := Run(context.Background(), []string{"status"}, env, nil); code != 1 {
			t.Fatalf("Run(status) = %d, want 1", code)
		}
		want := fmt.Sprintf("✗ Can't read %s.\n", state.StatePath(env.StateDir))
		if stdout.String() != want || stderr.Len() != 0 {
			t.Errorf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
		}
	})
}

func TestStatusSince(t *testing.T) {
	for seconds, want := range map[int]string{
		0: "less than a minute", 59: "less than a minute", 60: "1 minute", 119: "1 minute", 120: "2 minutes",
		3599: "59 minutes", 3600: "1 hour", 86399: "23 hours", 86400: "1 day",
	} {
		if got := sinceText(time.Duration(seconds) * time.Second); got != want {
			t.Errorf("sinceText(%d seconds) = %q, want %q", seconds, got, want)
		}
	}
}

func TestLogsCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		call []string
	}{
		{args: []string{"logs"}, call: []string{"journalctl", "--user", "-u", "tervi", "-o", "cat", "--no-pager"}},
		{args: []string{"logs", "-f"}, call: []string{"journalctl", "--user", "-u", "tervi", "-o", "cat", "--no-pager", "-f"}},
	} {
		runner := &commandRunner{output: "daemon log\n"}
		client := &commandControl{}
		env, _, stdout, stderr := commandEnv(t, runner, client, true)
		if code := Run(context.Background(), tc.args, env, nil); code != 0 {
			t.Errorf("Run(%v) = %d, want 0", tc.args, code)
		}
		if fmt.Sprint(runner.streams) != fmt.Sprint([][]string{tc.call}) || stdout.String() != "daemon log\n" || stderr.Len() != 0 {
			t.Errorf("streams=%v stdout=%q stderr=%q", runner.streams, stdout.String(), stderr.String())
		}
	}
	var observed []string
	runner := &commandRunner{chunks: []string{"new line\n", "later line\n"}}
	env, _, stdout, stderr := commandEnv(t, runner, &commandControl{}, true)
	runner.streamCheck = func() { observed = append(observed, stdout.String()) }
	if code := Run(context.Background(), []string{"logs", "-f"}, env, nil); code != 0 {
		t.Errorf("Run(logs -f) = %d, want 0", code)
	}
	if got, want := observed, []string{"new line\n", "new line\nlater line\n"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stream visibility snapshots = %q, want %q", got, want)
	}
	if stdout.String() != "new line\nlater line\n" || stderr.Len() != 0 {
		t.Errorf("streamed output = (%q, %q), want exact stream and empty stderr", stdout.String(), stderr.String())
	}

	runner = &commandRunner{streamError: errors.New("journal unavailable")}
	env, _, stdout, stderr = commandEnv(t, runner, &commandControl{}, true)
	if code := Run(context.Background(), []string{"logs"}, env, nil); code != 1 || stdout.String() != "✗ Can't read the log: journal unavailable\n" || stderr.Len() != 0 {
		t.Errorf("failed logs = (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
	runner = &commandRunner{output: "journalctl: journal is unavailable\n", streamError: &exec.ExitError{}}
	env, _, stdout, stderr = commandEnv(t, runner, &commandControl{}, true)
	if code := Run(context.Background(), []string{"logs"}, env, nil); code != 1 || stdout.String() != "journalctl: journal is unavailable\n" || stderr.Len() != 0 {
		t.Errorf("journalctl exit error = (%d, %q, %q), want exit 1 with only journalctl output", code, stdout.String(), stderr.String())
	}
	runner = &commandRunner{}
	env, _, stdout, stderr = commandEnv(t, runner, &commandControl{}, true)
	usage := usageLine + "\n"
	if code := Run(context.Background(), []string{"logs", "extra"}, env, nil); code != 2 || stdout.Len() != 0 || stderr.String() != usage {
		t.Errorf("wrong logs usage = (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
}

func TestNoCredentialPrinted(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		runner := &commandRunner{}
		client := &commandControl{statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080"}}}, connectErr: errors.New(testCredential)}
		env, _, stdout, stderr := commandEnv(t, runner, client, true)
		Run(context.Background(), []string{"connect"}, env, nil)
		assertNoCredential(t, stdout.String(), stderr.String())
	})
	t.Run("status", func(t *testing.T) {
		runner := &commandRunner{}
		client := &commandControl{statuses: []statusResult{{answer: control.StatusAnswer{State: "reconnecting", Server: "http://localhost:8080", LastError: ptrString(testCredential)}}}}
		env, _, stdout, stderr := commandEnv(t, runner, client, true)
		Run(context.Background(), []string{"status"}, env, nil)
		assertNoCredential(t, stdout.String(), stderr.String())
	})
	t.Run("connected status server", func(t *testing.T) {
		runner := &commandRunner{}
		client := &commandControl{statuses: []statusResult{{answer: control.StatusAnswer{State: "connected", Server: testCredential, Since: time.Now().Format(time.RFC3339)}}}}
		env, _, stdout, stderr := commandEnv(t, runner, client, true)
		Run(context.Background(), []string{"status"}, env, nil)
		assertNoCredential(t, stdout.String(), stderr.String())
	})
	t.Run("logs", func(t *testing.T) {
		const stream = "daemon log has no credential\n"
		runner := &commandRunner{output: stream}
		env, _, stdout, stderr := commandEnv(t, runner, &commandControl{}, true)
		if code := Run(context.Background(), []string{"logs"}, env, nil); code != 0 {
			t.Errorf("Run(logs) = %d, want 0", code)
		}
		if stdout.String() != stream {
			t.Errorf("logs output = %q, want stream %q", stdout.String(), stream)
		}
		assertNoCredential(t, stdout.String(), stderr.String())
	})
}

func TestPairingUnreadable(t *testing.T) {
	for _, failure := range []string{"unreadable store", "damaged entry", "unreadable state"} {
		for _, command := range []string{"connect", "status"} {
			t.Run(failure+"/"+command, func(t *testing.T) {
				runner := &commandRunner{}
				client := &commandControl{}
				env, backend, stdout, stderr := commandEnv(t, runner, client, true)
				switch failure {
				case "unreadable store":
					backend.FailGet = true
				case "damaged entry":
					_ = backend.Set("worker", "not json")
				case "unreadable state":
					if err := os.Remove(state.StatePath(env.StateDir)); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(state.StatePath(env.StateDir), 0700); err != nil {
						t.Fatal(err)
					}
				}
				before, _ := os.ReadFile(state.StatePath(env.StateDir))
				if code := Run(context.Background(), []string{command}, env, nil); code != 1 {
					t.Fatalf("Run(%s) = %d, want 1", command, code)
				}
				want := fmt.Sprintf("✗ Can't read %s.\n", credential.Path(env.StateDir))
				if failure == "unreadable state" {
					want = fmt.Sprintf("✗ Can't read %s.\n", state.StatePath(env.StateDir))
				}
				if stdout.String() != want || stderr.Len() != 0 || len(runner.calls) != 0 || client.connectCalls != 0 {
					t.Errorf("stdout=%q stderr=%q systemctl=%v", stdout.String(), stderr.String(), runner.calls)
				}
				if failure != "unreadable state" {
					after, _ := os.ReadFile(state.StatePath(env.StateDir))
					if !bytes.Equal(before, after) {
						t.Error("state file changed")
					}
				}
				if _, err := os.Stat(filepath.Join(env.ServiceDir, "tervi.service")); !os.IsNotExist(err) {
					t.Errorf("service file was written, stat error = %v", err)
				}
			})
		}
	}
}

func TestMissingInternalSetting(t *testing.T) {
	for _, field := range []string{"Runner", "ControlClient", "ProgramPath", "ServiceDir"} {
		t.Run(field, func(t *testing.T) {
			runner := &commandRunner{}
			client := &commandControl{}
			env, _, stdout, stderr := commandEnv(t, runner, client, true)
			switch field {
			case "Runner":
				env.Runner = nil
			case "ControlClient":
				env.ControlClient = nil
			case "ProgramPath":
				env.ProgramPath = ""
			case "ServiceDir":
				env.ServiceDir = ""
			}
			if code := Run(context.Background(), []string{"connect"}, env, nil); code != 1 {
				t.Fatalf("Run(connect) = %d, want 1", code)
			}
			want := fmt.Sprintf("✗ tervi is missing an internal setting (%s).\n", field)
			if stdout.String() != want || stderr.Len() != 0 || len(runner.calls) != 0 {
				t.Errorf("stdout=%q stderr=%q calls=%v, want %q", stdout.String(), stderr.String(), runner.calls, want)
			}
		})
	}
	for _, tc := range []struct {
		command string
		field   string
	}{
		{command: "status", field: "ControlClient"},
		{command: "logs", field: "Runner"},
		{command: "pair", field: "ProgramPath"},
	} {
		t.Run(tc.command+"/"+tc.field, func(t *testing.T) {
			runner := &commandRunner{}
			client := &commandControl{}
			env, _, stdout, stderr := commandEnv(t, runner, client, true)
			if tc.field == "Runner" {
				env.Runner = nil
			} else {
				env.ControlClient = nil
			}
			args := []string{tc.command}
			if tc.command == "pair" {
				args = []string{"pair", "--server", "http://localhost:8080"}
				env.ProgramPath = ""
				stateFile := state.StatePath(env.StateDir)
				if err := os.Remove(stateFile); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(stateFile, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if code := Run(context.Background(), args, env, nil); code != 1 || stdout.String() != fmt.Sprintf("✗ tervi is missing an internal setting (%s).\n", tc.field) || stderr.Len() != 0 {
				t.Errorf("Run(%s) = (%d, %q, %q)", tc.command, code, stdout.String(), stderr.String())
			}
			if tc.command == "pair" && (len(runner.calls) != 0 || client.statusCalls != 0 || client.connectCalls != 0) {
				t.Errorf("pair with missing ProgramPath did work: systemctl=%v status=%d connect=%d", runner.calls, client.statusCalls, client.connectCalls)
			}
		})
	}
}

func ptrString(value string) *string { return &value }

var _ service.Runner = (*commandRunner)(nil)
