package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const pairServer = "http://localhost:8080"
const replacementCredential = "new-credential-for-repair"

type successfulPairFlow struct {
	starts, finishes int
	server           string
	entry            credential.Entry
	credential       string
	err              error
	inspectStart     bool
	stateAtStart     bool
	entryAtStart     bool
	inspectStartErr  error
}

type requestingPairFlow struct{}

func (requestingPairFlow) StartNew(_ context.Context, _ Env, server string) int {
	response, err := http.Get(server)
	if err == nil {
		_ = response.Body.Close()
	}
	return 1
}

func (requestingPairFlow) FinishEarlier(context.Context, Env, credential.Entry) int { return 1 }

func (flow *successfulPairFlow) StartNew(_ context.Context, env Env, server string) int {
	flow.starts++
	flow.server = server
	if flow.inspectStart {
		_, err := os.Stat(state.StatePath(env.StateDir))
		flow.stateAtStart = err == nil
		if err != nil && !os.IsNotExist(err) {
			flow.inspectStartErr = err
		}
		_, flow.entryAtStart, err = env.Store.Get()
		if err != nil && flow.inspectStartErr == nil {
			flow.inspectStartErr = err
		}
	}
	value := flow.credential
	if value == "" {
		value = testCredential
	}
	if err := flow.save(env, server, value); err != nil {
		flow.err = err
		return 1
	}
	if env.OnPairingSuccess != nil {
		env.OnPairingSuccess(credential.Entry{Server: server, Credential: secret.FromString(value)})
	}
	fmt.Fprintln(env.Stdout, "✓ Paired successfully.")
	return 0
}

func (flow *successfulPairFlow) FinishEarlier(_ context.Context, env Env, entry credential.Entry) int {
	flow.finishes++
	flow.entry = entry
	record, exists, err := state.ReadState(env.StateDir)
	if err != nil || !exists {
		flow.err = fmt.Errorf("read pairing state: %v", err)
		return 1
	}
	record.Confirmed = true
	if err := state.WriteState(env.StateDir, record); err != nil {
		flow.err = err
		return 1
	}
	fmt.Fprintln(env.Stdout, "✓ Paired successfully.")
	return 0
}

func (flow *successfulPairFlow) save(env Env, server, value string) error {
	if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true, Confirmed: true}); err != nil {
		return err
	}
	return env.Store.Save(credential.Entry{Server: server, Credential: secret.FromString(value)})
}

func newPairHarness(t *testing.T, paired bool) (Env, *commandRunner, *commandControl, *credential.MemoryBackend, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	runner := &commandRunner{}
	client := &commandControl{connect: control.ConnectAnswer{Result: "connected"}}
	env, backend, stdout, stderr := commandEnv(t, runner, client, paired)
	env.ProgramPath = "/usr/bin/true"
	env.ServiceDir = t.TempDir()
	return env, runner, client, backend, stdout, stderr
}

func prepareRejectedPairing(t *testing.T, env Env, backend *credential.MemoryBackend) {
	t.Helper()
	if err := state.WriteState(env.StateDir, state.State{
		MachineID: "machine-123", CredentialSaved: true, Confirmed: true, StopReason: ptrString("credential_rejected"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Set("worker", `{"server":"http://localhost:8080","credential":"`+testCredential+`"}`); err != nil {
		t.Fatal(err)
	}
}

func assertPairOutputSafe(t *testing.T, stdout, stderr string) {
	t.Helper()
	assertNoCredential(t, stdout, stderr)
	for _, secretValue := range []string{replacementCredential} {
		if strings.Contains(stdout, secretValue) || strings.Contains(stderr, secretValue) {
			t.Errorf("output exposed credential %q", secretValue)
		}
	}
}

func TestPairInstalledCheckFirst(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	temporaryProgram := filepath.Join(t.TempDir(), "tervi")
	if err := os.WriteFile(temporaryProgram, []byte("temporary"), 0700); err != nil {
		t.Fatal(err)
	}
	env, _, _, _, stdout, stderr := newPairHarness(t, false)
	env.ProgramPath = temporaryProgram
	flow := requestingPairFlow{}
	code := Run(context.Background(), []string{"pair", "--server", server.URL}, env, flow)
	want := installRequiredMessage + "\n"
	if code != 1 || stdout.String() != want || stderr.Len() != 0 || requests.Load() != 0 {
		t.Errorf("Run()=(%d,%q,%q), requests=%d; want installed message before network", code, stdout.String(), stderr.String(), requests.Load())
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairHandsOver(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env, runner, client, _, stdout, stderr := newPairHarness(t, false)
	client.connectFunc = func(context.Context) (control.ConnectAnswer, error) {
		if len(runner.calls) != 4 || !strings.Contains(stdout.String(), backgroundStartedMessage+"\n") {
			t.Errorf("Connect called before service start and background message: systemctl=%v output=%q", runner.calls, stdout.String())
		}
		return control.ConnectAnswer{Result: "connected"}, nil
	}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, want 0; output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	if flow.starts != 1 || len(runner.calls) != 4 || client.statusCalls != 1 || client.connectCalls != 1 {
		t.Errorf("start steps: flow=%+v systemctl=%v status=%d connect=%d", flow, runner.calls, client.statusCalls, client.connectCalls)
	}
	wantCalls := [][]string{
		{"loginctl", "enable-linger"},
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "tervi"},
		{"systemctl", "--user", "restart", "tervi"},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Errorf("systemctl calls = %v, want %v", runner.calls, wantCalls)
	}
	serviceFile := filepath.Join(env.ServiceDir, "tervi.service")
	contents, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("service file not written in ServiceDir: %v", err)
	}
	programPath, err := filepath.EvalSymlinks("/usr/bin/true")
	if err != nil {
		t.Fatalf("resolve /usr/bin/true: %v", err)
	}
	if !strings.Contains(string(contents), "ExecStart=\""+programPath+"\" daemon\n") {
		t.Errorf("service file uses wrong program: %q", contents)
	}
	otherServiceFile := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "tervi.service")
	if _, err := os.Stat(otherServiceFile); !os.IsNotExist(err) {
		t.Errorf("service file also appeared outside ServiceDir at %s: %v", otherServiceFile, err)
	}
	if got := strings.Index(stdout.String(), "✓ Paired successfully."); got < 0 || got > strings.Index(stdout.String(), "✓ tervi now runs") || strings.Index(stdout.String(), "✓ tervi now runs") > strings.Index(stdout.String(), "✓ Connected.") {
		t.Errorf("handover lines are out of order: %q", stdout.String())
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

type failWorkerGetBackend struct {
	*credential.MemoryBackend
	workerGets int
	failAt     int
}

func (backend *failWorkerGetBackend) Get(user string) (string, error) {
	if user == "worker" {
		backend.workerGets++
		if backend.workerGets == backend.failAt {
			return "", errors.New("unexpected second pairing entry read")
		}
	}
	return backend.MemoryBackend.Get(user)
}

func TestPairHandsOverUsesEntryFromPairingFlow(t *testing.T) {
	env, _, _, backend, stdout, stderr := newPairHarness(t, false)
	trackingBackend := &failWorkerGetBackend{MemoryBackend: backend, failAt: 3}
	env.Store = credential.NewStore(trackingBackend)
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	if trackingBackend.workerGets != 2 {
		t.Errorf("worker entry reads = %d, want only the preflight and pairing save verification", trackingBackend.workerGets)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairSetsConnectThroughDaemon(t *testing.T) {
	env, _, client, _, stdout, stderr := newPairHarness(t, false)
	client.connectFunc = func(context.Context) (control.ConnectAnswer, error) {
		record, exists, err := state.ReadState(env.StateDir)
		if err != nil || !exists {
			return control.ConnectAnswer{}, errors.New("pairing state missing")
		}
		record.Connect = true
		if err := state.WriteState(env.StateDir, record); err != nil {
			return control.ConnectAnswer{}, err
		}
		return control.ConnectAnswer{Result: "connected"}, nil
	}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output %q", code, stdout.String())
	}
	record, exists, err := state.ReadState(env.StateDir)
	if err != nil || !exists || !record.Connect || client.connectCalls != 1 {
		t.Errorf("daemon connect write: state=%+v exists=%v err=%v connect calls=%d", record, exists, err, client.connectCalls)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairStillTrying(t *testing.T) {
	for _, tc := range []struct {
		name string
		last *string
		line string
	}{
		{name: "last error", last: ptrString("connection refused"), line: "Paired, but not connected yet: connection refused. tervi keeps trying in the background.\n"},
		{name: "no last error", line: "Paired, but not connected yet. tervi keeps trying in the background.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, client, _, stdout, stderr := newPairHarness(t, false)
			client.connect = control.ConnectAnswer{Result: "still_trying", LastError: tc.last}
			flow := successfulPairFlow{}
			if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
				t.Errorf("Run() = %d, want 0; output=%q", code, stdout.String())
			}
			want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n" + tc.line + "  Check with: tervi status\n"
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
			}
			assertPairOutputSafe(t, stdout.String(), stderr.String())
		})
	}
}

func TestPairConnectFailed(t *testing.T) {
	env, _, client, backend, stdout, stderr := newPairHarness(t, false)
	client.connect = control.ConnectAnswer{Result: "failed", Path: state.StatePath(env.StateDir)}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
		t.Fatalf("Run() = %d, want 1; output=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "✗ Can't write "+state.StatePath(env.StateDir)+".\n") {
		t.Errorf("missing state write failure: %q", stdout.String())
	}
	if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
		t.Errorf("pairing state was not kept: exists=%v err=%v", exists, err)
	}
	if _, exists := backend.Value("worker"); !exists {
		t.Error("credential file entry was not kept")
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairAlreadyConnected(t *testing.T) {
	env, _, client, _, stdout, stderr := newPairHarness(t, false)
	client.connect = control.ConnectAnswer{Result: "already_connected"}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairServiceFails(t *testing.T) {
	env, runner, _, backend, stdout, stderr := newPairHarness(t, false)
	runner.runOutput = "systemd could not start"
	runner.runError = errors.New("command failed")
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
		t.Fatalf("Run() = %d, want 1; output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✗ Can't start tervi in the background: systemd could not start\n  Try again with: tervi connect\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
		t.Errorf("pairing state was not kept: exists=%v err=%v", exists, err)
	}
	if _, exists := backend.Value("worker"); !exists {
		t.Error("credential file entry was not kept")
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairNoRuntimeFolder(t *testing.T) {
	env, runner, _, backend, stdout, stderr := newPairHarness(t, false)
	t.Setenv("XDG_RUNTIME_DIR", "")
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
		t.Fatalf("Run() = %d, want 1; output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).\n"
	if stdout.String() != want || stderr.Len() != 0 || len(runner.calls) != 0 {
		t.Errorf("output=(%q,%q), systemctl=%v; want runtime failure and no systemctl", stdout.String(), stderr.String(), runner.calls)
	}
	if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
		t.Errorf("pairing state was not kept: exists=%v err=%v", exists, err)
	}
	if _, exists := backend.Value("worker"); !exists {
		t.Error("credential file entry was not kept")
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairFinishEarlierHandsOver(t *testing.T) {
	env, _, _, _, stdout, stderr := newPairHarness(t, true)
	if err := state.WriteState(env.StateDir, state.State{MachineID: "machine-123", CredentialSaved: true}); err != nil {
		t.Fatal(err)
	}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n"
	if flow.finishes != 1 || flow.starts != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("flow=%+v output=(%q,%q), want finish hand-over %q", flow, stdout.String(), stderr.String(), want)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairReturnsAfterLastLine(t *testing.T) {
	env, runner, _, _, stdout, stderr := newPairHarness(t, false)
	daemonRunning := false
	runner.runFunc = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			for _, arg := range args {
				switch arg {
				case "restart", "--now":
					daemonRunning = true
				case "stop":
					daemonRunning = false
				}
			}
		}
		return "", nil
	}
	client := env.ControlClient.(*commandControl)
	client.connectFunc = func(context.Context) (control.ConnectAnswer, error) {
		if !daemonRunning {
			return control.ConnectAnswer{}, errors.New("fake daemon was not started")
		}
		return control.ConnectAnswer{Result: "connected"}, nil
	}
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	if !daemonRunning {
		t.Error("fake daemon stopped before pair returned")
	}
	if stdout.String() != "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n" || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q)", stdout.String(), stderr.String())
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairAgainAfterRejection(t *testing.T) {
	env, _, _, backend, stdout, stderr := newPairHarness(t, false)
	prepareRejectedPairing(t, env, backend)
	flow := successfulPairFlow{credential: replacementCredential, inspectStart: true}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "The server no longer accepts this computer's credential. Starting a new pairing.\n") || flow.starts != 1 || flow.server != pairServer {
		t.Errorf("re-pair output=%q flow=%+v", stdout.String(), flow)
	}
	if flow.inspectStartErr != nil || flow.stateAtStart || flow.entryAtStart {
		t.Errorf("old pairing still existed when replacement flow started: state=%v entry=%v inspection error=%v", flow.stateAtStart, flow.entryAtStart, flow.inspectStartErr)
	}
	entry, exists, err := env.Store.Get()
	if err != nil || !exists || entry.Credential.Reveal() != replacementCredential {
		t.Errorf("new pairing entry=%+v exists=%v err=%v", entry, exists, err)
	}
	record, stateExists, err := state.ReadState(env.StateDir)
	if err != nil || !stateExists || !record.Confirmed || record.StopReason != nil {
		t.Errorf("new pairing state=%+v exists=%v err=%v", record, stateExists, err)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestAlreadyPairedStillRefused(t *testing.T) {
	env, runner, client, backend, stdout, stderr := newPairHarness(t, true)
	before, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	entryBefore, _ := backend.Value("worker")
	flow := successfulPairFlow{}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}
	want := "This computer is already paired with http://localhost:8080. Nothing was changed.\n"
	if stdout.String() != want || stderr.Len() != 0 || flow.starts+flow.finishes != 0 || len(runner.calls) != 0 || client.connectCalls != 0 {
		t.Errorf("output=(%q,%q), flow=%+v service=%v connect=%d", stdout.String(), stderr.String(), flow, runner.calls, client.connectCalls)
	}
	after, _ := os.ReadFile(state.StatePath(env.StateDir))
	entryAfter, entryExists := backend.Value("worker")
	if !bytes.Equal(before, after) || !entryExists || entryBefore != entryAfter {
		t.Error("already paired state or credential file entry changed")
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestPairAgainDeleteFails(t *testing.T) {
	t.Run("credential file", func(t *testing.T) {
		env, _, _, backend, stdout, stderr := newPairHarness(t, false)
		prepareRejectedPairing(t, env, backend)
		backend.FailDelete = true
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		want := fmt.Sprintf("The server no longer accepts this computer's credential. Starting a new pairing.\n✗ Can't delete %s.\n", credential.Path(env.StateDir))
		if stdout.String() != want || stderr.Len() != 0 || flow.starts != 0 {
			t.Errorf("output=(%q,%q), flow=%+v, want %q", stdout.String(), stderr.String(), flow, want)
		}
		if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
			t.Errorf("state should remain after secret delete failure: exists=%v err=%v", exists, err)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
	t.Run("state file", func(t *testing.T) {
		env, _, _, base, stdout, stderr := newPairHarness(t, false)
		prepareRejectedPairing(t, env, base)
		backend := &stateDeleteFailBackend{MemoryBackend: base, statePath: state.StatePath(env.StateDir)}
		env.Store = credential.NewStore(backend)
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		want := fmt.Sprintf("The server no longer accepts this computer's credential. Starting a new pairing.\n✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", state.StatePath(env.StateDir), env.StateDir)
		if stdout.String() != want || stderr.Len() != 0 || flow.starts != 0 {
			t.Errorf("output=(%q,%q), flow=%+v, want %q", stdout.String(), stderr.String(), flow, want)
		}
		if info, err := os.Stat(state.StatePath(env.StateDir)); err != nil || !info.IsDir() {
			t.Errorf("state deletion failure was not preserved: info=%v err=%v", info, err)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
}

type stateDeleteFailBackend struct {
	*credential.MemoryBackend
	statePath string
}

func (backend *stateDeleteFailBackend) Delete(user string) error {
	if err := backend.MemoryBackend.Delete(user); err != nil {
		return err
	}
	if err := os.Remove(backend.statePath); err != nil {
		return err
	}
	if err := os.Mkdir(backend.statePath, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(backend.statePath, "sentinel"), nil, 0600)
}

func TestHandOverCtrlC(t *testing.T) {
	env, _, client, backend, stdout, stderr := newPairHarness(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	client.connectFunc = func(context.Context) (control.ConnectAnswer, error) {
		cancel()
		return control.ConnectAnswer{}, context.Canceled
	}
	flow := successfulPairFlow{}
	if code := Run(ctx, []string{"pair", "--server", pairServer}, env, &flow); code != 130 {
		t.Errorf("Run() = %d, want 130; output=%q", code, stdout.String())
	}
	want := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\nPairing finished. Connecting was cancelled; tervi keeps trying in the background.\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
		t.Errorf("pairing state was not kept: exists=%v err=%v", exists, err)
	}
	if _, exists := backend.Value("worker"); !exists {
		t.Error("credential file entry was not kept")
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestNoCredentialInPairOutput(t *testing.T) {
	t.Run("still trying error containing credential", func(t *testing.T) {
		env, _, client, _, stdout, stderr := newPairHarness(t, false)
		client.connect = control.ConnectAnswer{Result: "still_trying", LastError: ptrString(testCredential)}
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
			t.Errorf("Run() = %d, want 0", code)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
	t.Run("connection error containing credential", func(t *testing.T) {
		env, _, client, _, stdout, stderr := newPairHarness(t, false)
		client.connectErr = errors.New(testCredential)
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
			t.Errorf("Run() = %d, want 1", code)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
	t.Run("service error containing credential", func(t *testing.T) {
		env, runner, _, _, stdout, stderr := newPairHarness(t, false)
		runner.runOutput = testCredential
		runner.runError = errors.New("failed")
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
			t.Errorf("Run() = %d, want 1", code)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
	t.Run("control path containing credential", func(t *testing.T) {
		env, _, client, _, stdout, stderr := newPairHarness(t, false)
		client.connect = control.ConnectAnswer{Result: "failed", Path: testCredential}
		flow := successfulPairFlow{}
		if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 1 {
			t.Errorf("Run() = %d, want 1", code)
		}
		assertPairOutputSafe(t, stdout.String(), stderr.String())
	})
}

func TestPairUnsuccessfulFlowDoesNotHandOver(t *testing.T) {
	env, runner, client, _, stdout, stderr := newPairHarness(t, false)
	flow := &flowRecorder{startCode: 1}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, flow); code != 1 {
		t.Errorf("Run() = %d, want 1", code)
	}
	if len(runner.calls) != 0 || client.connectCalls != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("unsuccessful flow handed over: output=(%q,%q), systemctl=%v connect=%d", stdout.String(), stderr.String(), runner.calls, client.connectCalls)
	}
}

func TestPairWrongUsagePrecedesInstalledCheck(t *testing.T) {
	env, runner, client, _, stdout, stderr := newPairHarness(t, false)
	env.ProgramPath = ""
	if code := Run(context.Background(), []string{"pair", "--unknown"}, env, nil); code != 2 {
		t.Errorf("Run() = %d, want 2", code)
	}
	if stdout.Len() != 0 || stderr.Len() == 0 || len(runner.calls) != 0 || client.connectCalls != 0 {
		t.Errorf("usage check did not precede installed check: output=(%q,%q), systemctl=%v connect=%d", stdout.String(), stderr.String(), runner.calls, client.connectCalls)
	}
}
