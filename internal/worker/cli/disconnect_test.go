package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/state"
)

type disconnectControl struct {
	answer       control.DisconnectAnswer
	err          error
	disconnects  int
	status       control.StatusAnswer
	statusErr    error
	connect      control.ConnectAnswer
	connectCalls int
}

func (client *disconnectControl) Status(context.Context) (control.StatusAnswer, error) {
	return client.status, client.statusErr
}

func (client *disconnectControl) Connect(context.Context) (control.ConnectAnswer, error) {
	client.connectCalls++
	return client.connect, nil
}

func (client *disconnectControl) Disconnect(context.Context) (control.DisconnectAnswer, error) {
	client.disconnects++
	return client.answer, client.err
}

func TestDisconnectConnected(t *testing.T) {
	runner := &commandRunner{}
	env, _, stdout, stderr := commandEnv(t, runner, &commandControl{}, true)
	client := &disconnectControl{answer: control.DisconnectAnswer{Result: daemon.ResultDisconnected}}
	env.ControlClient = client
	if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	code := runDisconnectWhileLockBusy(t, env)
	want := "✓ Disconnected. tervi stays in the background but won't connect.\n  Connect again with: tervi connect\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 || client.disconnects != 1 {
		t.Errorf("Run(disconnect) = %d, stdout=%q stderr=%q, calls=%d", code, stdout.String(), stderr.String(), client.disconnects)
	}
	if len(runner.calls) != 0 {
		t.Errorf("disconnect started or stopped a service: %v", runner.calls)
	}
}

func TestDisconnectNoDaemon(t *testing.T) {
	runner := &commandRunner{}
	env, _, stdout, stderr := commandEnv(t, runner, &commandControl{}, true)
	client := &disconnectControl{answer: control.DisconnectAnswer{Result: daemon.ResultDisconnected}}
	env.ControlClient = client
	before := state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: true}
	if err := state.WriteState(env.StateDir, before); err != nil {
		t.Fatal(err)
	}
	code := Run(context.Background(), []string{"disconnect"}, env, nil)
	want := "✓ Disconnected. tervi won't connect, including after the computer restarts.\n  Connect again with: tervi connect\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 || client.disconnects != 0 {
		t.Errorf("Run(disconnect) = %d, stdout=%q stderr=%q, calls=%d", code, stdout.String(), stderr.String(), client.disconnects)
	}
	got, _, err := state.ReadState(env.StateDir)
	if err != nil || got.Connect || got.StopReason != nil || got.MachineID != before.MachineID || !got.Confirmed || !got.CredentialSaved {
		t.Errorf("state = %+v, err %v; want pairing fields preserved and connect false", got, err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("disconnect started a service: %v", runner.calls)
	}
}

func TestDisconnectAlready(t *testing.T) {
	for _, tc := range []struct {
		name       string
		busy       bool
		stopReason *string
	}{
		{name: "without daemon"},
		{name: "with daemon", busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
			client := &disconnectControl{answer: control.DisconnectAnswer{Result: "already_disconnected"}}
			env.ControlClient = client
			before := state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: false}
			if err := state.WriteState(env.StateDir, before); err != nil {
				t.Fatal(err)
			}
			beforeBytes, err := os.ReadFile(state.StatePath(env.StateDir))
			if err != nil {
				t.Fatal(err)
			}
			code := 0
			if tc.busy {
				code = runDisconnectWhileLockBusy(t, env)
			} else {
				code = Run(context.Background(), []string{"disconnect"}, env, nil)
			}
			if code != 0 || stdout.String() != "Already disconnected.\n" || stderr.Len() != 0 {
				t.Errorf("Run(disconnect) = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			after, err := os.ReadFile(state.StatePath(env.StateDir))
			if err != nil || string(after) != string(beforeBytes) {
				t.Errorf("state changed: err=%v before=%q after=%q", err, beforeBytes, after)
			}
		})
	}
}

func TestDisconnectRejectedOrRevoked(t *testing.T) {
	for _, reason := range []string{state.StopReasonCredentialRejected, state.StopReasonCredentialRevoked} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/busy=%v", reason, busy), func(t *testing.T) {
				env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
				client := &disconnectControl{answer: control.DisconnectAnswer{Result: reasonResult(reason)}}
				env.ControlClient = client
				if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, StopReason: &reason}); err != nil {
					t.Fatal(err)
				}
				code := 0
				if busy {
					code = runDisconnectWhileLockBusy(t, env)
				} else {
					code = Run(context.Background(), []string{"disconnect"}, env, nil)
				}
				want := "✗ Not connecting: the server doesn't accept this computer's credential.\n  Pair again with: tervi pair --server http://localhost:8080\n"
				if reason == state.StopReasonCredentialRevoked {
					want = "✗ This computer was unpaired on the server.\n  Pair it again with: tervi pair --server http://localhost:8080\n"
				}
				if code != 0 || stdout.String() != want || stderr.Len() != 0 {
					t.Errorf("Run(disconnect) = %d, stdout=%q stderr=%q, want %q", code, stdout.String(), stderr.String(), want)
				}
				if got, _, err := state.ReadState(env.StateDir); err != nil || got.StopReason == nil || *got.StopReason != reason {
					t.Errorf("state changed: %+v, err %v", got, err)
				}
			})
		}
	}
}

func TestDisconnectNotPaired(t *testing.T) {
	env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, false)
	if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 1 {
		t.Fatalf("Run(disconnect) = %d, want 1", code)
	}
	want := "This computer isn't paired. Pair it with: tervi pair --server <url>\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
	}
}

func TestDisconnectThenStatus(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("daemon=%v", busy), func(t *testing.T) {
			env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
			client := &disconnectControl{
				answer: control.DisconnectAnswer{Result: daemon.ResultDisconnected},
				status: control.StatusAnswer{State: daemon.StateNotConnecting},
			}
			if !busy {
				client.statusErr = control.ErrNotRunning
			}
			env.ControlClient = client
			if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
				t.Fatal(err)
			}
			if busy {
				if code := runDisconnectWhileLockBusy(t, env); code != 0 {
					t.Fatalf("disconnect exit = %d", code)
				}
			} else if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 0 {
				t.Fatalf("disconnect exit = %d", code)
			}
			stdout.Reset()
			if code := Run(context.Background(), []string{"status"}, env, nil); code != 1 {
				t.Errorf("Run(status) = %d, want 1", code)
			}
			want := "○ Disconnected.\n  Connect with: tervi connect\n"
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("status stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
			}
		})
	}
}

func TestConnectAfterDisconnectCommand(t *testing.T) {
	runner := &commandRunner{}
	client := &disconnectControl{connect: control.ConnectAnswer{Result: daemon.ResultConnected}}
	env, _, stdout, stderr := commandEnv(t, runner, &commandControl{}, true)
	env.ControlClient = client
	client.status = control.StatusAnswer{State: daemon.StateReconnecting, Server: "http://localhost:8080"}
	if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 0 {
		t.Fatalf("disconnect exit = %d", code)
	}
	stdout.Reset()
	client.status = control.StatusAnswer{State: daemon.StateReconnecting, Server: "http://localhost:8080"}
	if code := Run(context.Background(), []string{"connect"}, env, nil); code != 0 {
		t.Errorf("Run(connect) = %d, want 0", code)
	}
	if stdout.String() != "✓ Connected.\n" || stderr.Len() != 0 || client.connectCalls != 1 {
		t.Errorf("connect stdout=%q stderr=%q calls=%d", stdout.String(), stderr.String(), client.connectCalls)
	}
}

func TestDisconnectWriteFailure(t *testing.T) {
	for _, withDaemon := range []bool{false, true} {
		t.Run(fmt.Sprintf("daemon=%v", withDaemon), func(t *testing.T) {
			env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
			if withDaemon {
				client := &disconnectControl{answer: control.DisconnectAnswer{Result: daemon.ResultFailed, Path: state.StatePath(env.StateDir)}}
				env.ControlClient = client
				if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
					t.Fatal(err)
				}
				if code := runDisconnectWhileLockBusy(t, env); code != 1 {
					t.Fatalf("Run(disconnect) = %d, want 1", code)
				}
			} else {
				env.StateDir = makeLongStateDir(t)
				data := []byte(`{"machine_id":"machine-123","credential_saved":true,"confirmed":true,"connect":true}`)
				if err := os.WriteFile(state.StatePath(env.StateDir), data, 0600); err != nil {
					t.Fatalf("write test state: %v (dir length %d)", err, len(env.StateDir))
				}
				if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 1 {
					t.Fatalf("Run(disconnect) = %d, want 1", code)
				}
			}
			want := fmt.Sprintf("✗ Can't write %s.\n", state.StatePath(env.StateDir))
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
			}
		})
	}
}

func TestDisconnectUnreadableAnswer(t *testing.T) {
	env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
	path := state.StatePath(env.StateDir)
	client := &disconnectControl{answer: control.DisconnectAnswer{Result: "unreadable", Path: path}}
	env.ControlClient = client
	if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	if code := runDisconnectWhileLockBusy(t, env); code != 1 {
		t.Fatalf("Run(disconnect) = %d, want 1", code)
	}
	want := fmt.Sprintf("✗ Can't read %s.\n", path)
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
	}
}

func makeLongStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for len(dir) < 4080 {
		remaining := 4080 - len(dir) - 1
		componentLength := remaining
		if componentLength > 200 {
			componentLength = 200
		}
		if componentLength < 1 {
			t.Fatalf("cannot create long state directory from path of length %d", len(dir))
		}
		dir = filepath.Join(dir, strings.Repeat("d", componentLength))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatalf("create long state directory (length %d): %v", len(dir), err)
		}
	}
	return dir
}

func TestDisconnectLockFailure(t *testing.T) {
	env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
	if err := os.MkdirAll(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tervi"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tervi", "daemon.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 1 {
		t.Fatalf("Run(disconnect) = %d, want 1", code)
	}
	if got := stdout.String(); len(got) < len("✗ Can't reach tervi in the background: ") || got[:len("✗ Can't reach tervi in the background: ")] != "✗ Can't reach tervi in the background: " {
		t.Errorf("stdout=%q, want a control-lock failure", got)
	}
	after, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil || string(before) != string(after) || stderr.Len() != 0 {
		t.Errorf("state changed or stderr not empty: err=%v before=%q after=%q stderr=%q", err, before, after, stderr.String())
	}
}

func TestDisconnectNoRuntimeFolder(t *testing.T) {
	env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
	if err := os.Unsetenv("XDG_RUNTIME_DIR"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"disconnect"}, env, nil); code != 1 {
		t.Fatalf("Run(disconnect) = %d, want 1", code)
	}
	want := "✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
	}
	after, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil || string(before) != string(after) {
		t.Errorf("state changed: err=%v before=%q after=%q", err, before, after)
	}
}

func runDisconnectWhileLockBusy(t *testing.T, env Env) int {
	t.Helper()
	runtimeDir, err := control.RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	code := -1
	if err := control.WithLock(runtimeDir, func() error {
		code = Run(context.Background(), []string{"disconnect"}, env, nil)
		return nil
	}); err != nil {
		t.Fatalf("hold lock around command: %v", err)
	}
	return code
}

func reasonResult(reason string) string {
	if reason == state.StopReasonCredentialRevoked {
		return daemon.ResultRevoked
	}
	return daemon.ResultRejected
}
