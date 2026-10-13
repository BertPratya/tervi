package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/pair"
	workerstate "github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

const connectionWait = 5 * time.Second

type connectedFixture struct {
	t              *testing.T
	pool           *pgxpool.Pool
	serverURL      string
	serverAddress  string
	server         *httptest.Server
	connections    *connection.List
	store          *credential.Store
	stateDir       string
	serviceDir     string
	daemonLogs     *safeBuffer
	daemonStderr   *safeBuffer
	serverLogs     *safeBuffer
	daemonCtx      context.Context
	cancelDaemon   context.CancelFunc
	runner         *fakeBackgroundRunner
	controlClient  control.Client
	workerEnv      cli.Env
	workerRuns     []*workerRun
	machineAnswers []string
	pageAnswers    []string
	statusAnswers  []string
	stateSnapshots []string
}

type backgroundDaemon struct {
	done     chan int
	finished chan struct{}
	exitCode int
}

type backgroundCall struct {
	name string
	args []string
}

type fakeBackgroundRunner struct {
	mu      sync.Mutex
	ctx     context.Context
	env     cli.Env
	calls   []backgroundCall
	daemons []*backgroundDaemon
}

func (runner *fakeBackgroundRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := backgroundCall{name: name, args: append([]string(nil), args...)}
	runner.mu.Lock()
	runner.calls = append(runner.calls, call)
	startDaemon := name == "systemctl" && isDaemonStart(args)
	runner.mu.Unlock()
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if startDaemon {
		daemon := &backgroundDaemon{done: make(chan int, 1), finished: make(chan struct{})}
		runner.mu.Lock()
		runner.daemons = append(runner.daemons, daemon)
		runner.mu.Unlock()
		go func() {
			daemon.exitCode = cli.Run(runner.ctx, []string{"daemon"}, runner.env, nil)
			daemon.done <- daemon.exitCode
			close(daemon.finished)
		}()
	}
	return "", nil
}

func (runner *fakeBackgroundRunner) Stream(context.Context, io.Writer, string, ...string) error {
	return nil
}

func (runner *fakeBackgroundRunner) callsSnapshot() []backgroundCall {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]backgroundCall(nil), runner.calls...)
}

func (runner *fakeBackgroundRunner) waitForDaemons(t *testing.T) {
	t.Helper()
	runner.mu.Lock()
	daemons := append([]*backgroundDaemon(nil), runner.daemons...)
	runner.mu.Unlock()
	for _, done := range daemons {
		select {
		case <-done.finished:
		case <-time.After(connectionWait):
			t.Error("fake service daemon did not exit after fixture cleanup")
		}
	}
}

func (runner *fakeBackgroundRunner) waitForDaemonExit(t *testing.T, index int, timeout time.Duration) int {
	t.Helper()
	runner.mu.Lock()
	if index >= len(runner.daemons) {
		runner.mu.Unlock()
		t.Fatalf("fake service started %d daemons, want one at index %d", len(runner.daemons), index)
	}
	daemon := runner.daemons[index]
	runner.mu.Unlock()
	select {
	case <-daemon.finished:
		return daemon.exitCode
	case <-time.After(timeout):
		t.Fatalf("fake daemon %d did not exit before timeout", index+1)
		return -1
	}
}

func (runner *fakeBackgroundRunner) daemonDoneIsEmpty(index int) bool {
	runner.mu.Lock()
	if index >= len(runner.daemons) {
		runner.mu.Unlock()
		return false
	}
	daemon := runner.daemons[index]
	runner.mu.Unlock()
	select {
	case <-daemon.finished:
		return false
	default:
		return len(daemon.done) == 0
	}
}

func isDaemonStart(args []string) bool {
	joined := strings.Join(args, " ")
	return joined == "--user restart tervi" || joined == "--user enable --now tervi"
}

func newConnectedFixture(t *testing.T) *connectedFixture {
	t.Helper()
	pool := dbtest.New(t)
	runtimeBase := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeBase)
	stateDir := filepath.Join(t.TempDir(), "state")
	daemonLogs, daemonStderr := &safeBuffer{}, &safeBuffer{}
	serverLogs := &safeBuffer{}
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	store := credential.NewStore(credential.NewMemoryBackend())
	runtimeDir, err := control.RuntimeDir()
	if err != nil {
		t.Fatalf("resolve test runtime directory: %v", err)
	}
	f := &connectedFixture{
		t: t, pool: pool, store: store, stateDir: stateDir, serviceDir: t.TempDir(),
		daemonLogs: daemonLogs, daemonStderr: daemonStderr, serverLogs: serverLogs,
		daemonCtx: daemonCtx, cancelDaemon: cancelDaemon,
		controlClient: control.Client{RuntimeDir: runtimeDir},
	}
	f.runner = &fakeBackgroundRunner{ctx: daemonCtx, env: cli.Env{
		Stdout: daemonLogs, Stderr: daemonStderr, Store: store, StateDir: stateDir,
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancelDaemon()
		t.Fatalf("open test server listener: %v", err)
	}
	f.serverAddress = listener.Addr().String()
	f.serverURL = "http://" + f.serverAddress
	f.startServer(listener)
	f.workerEnv = cli.Env{
		Stdout: &safeBuffer{}, Stderr: &safeBuffer{}, Store: store, StateDir: stateDir,
		ProgramPath: "/usr/bin/true", Runner: f.runner, ServiceDir: f.serviceDir,
		ControlClient: f.controlClient,
	}
	t.Cleanup(f.close)
	return f
}

func (f *connectedFixture) startServer(listener net.Listener) {
	f.t.Helper()
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", f.serverAddress)
		if err != nil {
			f.t.Fatalf("reopen test server listener: %v", err)
		}
	}
	f.connections = connection.NewList()
	logger := slog.New(slog.NewTextHandler(f.serverLogs, nil))
	handler := server.New(server.Config{
		PublicURL: f.serverURL, CheckInterval: time.Second, ConnectionTimeout: 3 * time.Second,
	}, f.pool, logger, f.connections)
	f.server = httptest.NewUnstartedServer(handler)
	f.server.Listener = listener
	f.server.Start()
}

func (f *connectedFixture) stopServer() {
	if f.server == nil {
		return
	}
	f.connections.CloseAll()
	f.server.Close()
	f.server = nil
	f.connections = nil
}

func (f *connectedFixture) close() {
	f.cancelDaemon()
	f.stopServer()
	f.runner.waitForDaemons(f.t)
}

func (f *connectedFixture) startPair(parent context.Context) *workerRun {
	f.t.Helper()
	stdin, input := io.Pipe()
	stdout, stderr := &safeBuffer{}, &safeBuffer{}
	run := &workerRun{input: input, stdout: stdout, stderr: stderr, done: make(chan int, 1)}
	env := f.workerEnv
	env.Stdin, env.Stdout, env.Stderr = stdin, stdout, stderr
	f.workerRuns = append(f.workerRuns, run)
	go func() {
		run.done <- cli.Run(parent, []string{"pair", "--server", f.serverURL}, env,
			pair.New(pair.Options{RequestTimeout: 2 * time.Second, PollInterval: 10 * time.Millisecond}))
		_ = stdin.Close()
	}()
	return run
}

func (f *connectedFixture) status(t *testing.T) (int, string) {
	t.Helper()
	stdout, stderr := &safeBuffer{}, &safeBuffer{}
	env := f.workerEnv
	env.Stdout, env.Stderr = stdout, stderr
	code := cli.Run(context.Background(), []string{"status"}, env, nil)
	if stderr.String() != "" {
		t.Errorf("tervi status wrote to stderr: %q", stderr.String())
	}
	f.statusAnswers = append(f.statusAnswers, stdout.String())
	return code, stdout.String()
}

func (f *connectedFixture) machineList(t *testing.T) []byte {
	t.Helper()
	response, err := http.Get(f.serverURL + "/api/v1/machines")
	if err != nil {
		t.Fatalf("GET machines list: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read machines list: %v", err)
	}
	f.machineAnswers = append(f.machineAnswers, string(body))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET machines list returned HTTP %d", response.StatusCode)
	}
	return body
}

func (f *connectedFixture) machinesPage(t *testing.T) string {
	t.Helper()
	response, err := http.Get(f.serverURL + "/machines")
	if err != nil {
		t.Fatalf("GET machines page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read machines page: %v", err)
	}
	f.pageAnswers = append(f.pageAnswers, string(body))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET machines page returned HTTP %d", response.StatusCode)
	}
	return string(body)
}

func (f *connectedFixture) stateSnapshot(t *testing.T) workerstate.State {
	t.Helper()
	data, err := os.ReadFile(workerstate.StatePath(f.stateDir))
	if err != nil {
		t.Fatalf("read state.json snapshot: %v", err)
	}
	f.stateSnapshots = append(f.stateSnapshots, string(data))
	var record workerstate.State
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode state.json snapshot: %v", err)
	}
	return record
}

func (f *connectedFixture) audit(credentials ...string) connectionAudit {
	var stdout, stderr strings.Builder
	for _, run := range f.workerRuns {
		stdout.WriteString(run.stdout.String())
		stderr.WriteString(run.stderr.String())
	}
	parts := []string{stdout.String(), stderr.String(), f.daemonLogs.String(), f.daemonStderr.String(), f.serverLogs.String()}
	parts = append(parts, f.machineAnswers...)
	parts = append(parts, f.pageAnswers...)
	parts = append(parts, f.statusAnswers...)
	parts = append(parts, f.stateSnapshots...)
	return connectionAudit{credentials: append([]string(nil), credentials...), contents: strings.Join(parts, "\n")}
}

type connectionAudit struct {
	credentials []string
	contents    string
}

type liveMachine struct {
	MachineID          string `json:"machine_id"`
	DisplayName        string `json:"display_name"`
	OSName             string `json:"os_name"`
	OSVersion          string `json:"os_version"`
	Online             bool   `json:"online"`
	LastSeenSecondsAgo *int64 `json:"last_seen_seconds_ago"`
}

type liveMachineAnswer struct {
	Machines []liveMachine `json:"machines"`
}

func decodeLiveMachines(t *testing.T, body []byte) liveMachineAnswer {
	t.Helper()
	var answer liveMachineAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode machines list: %v", err)
	}
	return answer
}

func (f *connectedFixture) machine(t *testing.T, id string) liveMachine {
	t.Helper()
	answer := decodeLiveMachines(t, f.machineList(t))
	for _, machine := range answer.Machines {
		if machine.MachineID == id {
			return machine
		}
	}
	t.Fatalf("machines list omitted paired machine %q", id)
	return liveMachine{}
}

func pairLiveMachine(t *testing.T, f *connectedFixture) livePair {
	t.Helper()
	return completeLivePair(t, f, f.startPair(context.Background()))
}

func completeLivePair(t *testing.T, f *connectedFixture, run *workerRun) livePair {
	t.Helper()
	approvalKey := waitForApprovalKey(t, run.stdout)
	pageResponse, err := http.Get(f.serverURL + "/pair/" + approvalKey)
	if err != nil {
		t.Fatalf("GET approval page: %v", err)
	}
	_ = pageResponse.Body.Close()
	if pageResponse.StatusCode != http.StatusOK {
		t.Fatalf("approval page returned HTTP %d", pageResponse.StatusCode)
	}
	approval := approvalRequest(t, f.serverURL, approvalKey, http.MethodGet, "")
	if approval.Status != "waiting_for_approval" {
		t.Fatalf("initial approval status = %q, want waiting_for_approval", approval.Status)
	}
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/accept")
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "waiting_for_code")
	if approval.PairingCode == "" {
		t.Fatal("approval API omitted pairing code")
	}
	waitFor(t, run.stdout, "Code: ", connectionWait)
	writeCode(t, run, approval.PairingCode, connectionWait)
	if code := awaitRun(t, run, connectionWait); code != 0 {
		t.Fatalf("pair worker exit = %d", code)
	}
	wantOutput := "✓ Paired successfully.\n✓ tervi now runs in the background and starts when you log in.\n✓ Connected.\n"
	if !strings.HasSuffix(run.stdout.String(), wantOutput) {
		t.Error("pair output did not end with the required connection messages")
	}
	waitForLogCount(t, f.daemonLogs, "✓ Connected to "+f.serverURL, len(f.workerRuns), connectionWait)
	state := f.stateSnapshot(t)
	if !state.CredentialSaved || !state.Confirmed || state.MachineID == "" || !state.Connect || state.StopReason != nil {
		t.Errorf("state after connect = %+v, want saved confirmed pairing, non-empty machine ID, connect true, and no stop reason", state)
	}
	entry, exists, err := f.store.Get()
	if err != nil || !exists || entry.Credential.Reveal() == "" {
		t.Fatalf("secret store did not contain the paired credential: exists=%t err=%v", exists, err)
	}
	machine := f.machine(t, state.MachineID)
	if !machine.Online {
		t.Errorf("paired machine online = false, want true")
	}
	if machine.DisplayName == "" || machine.OSName == "" || machine.OSVersion == "" {
		t.Errorf("machines list omitted machine details: %+v", machine)
	}
	return livePair{approvalKey: approvalKey, pairingCode: approval.PairingCode, credential: entry.Credential.Reveal(), machineID: state.MachineID, run: run}
}

type livePair struct {
	approvalKey string
	pairingCode string
	credential  string
	machineID   string
	run         *workerRun
}

func waitForLogCount(t *testing.T, output *safeBuffer, wanted string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(output.String(), wanted) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log contained %q %d times, want at least %d", wanted, strings.Count(output.String(), wanted), count)
}

func waitForControlState(t *testing.T, client control.Client, wanted string, timeout time.Duration) control.StatusAnswer {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		answer, err := client.Status(context.Background())
		if err == nil && answer.State == wanted {
			return answer
		}
		time.Sleep(10 * time.Millisecond)
	}
	answer, err := client.Status(context.Background())
	t.Fatalf("daemon status = %q, error=%v; want %q", answer.State, err, wanted)
	return control.StatusAnswer{}
}

func waitForMachineOnline(t *testing.T, f *connectedFixture, id string, online bool, timeout time.Duration) liveMachine {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		machine := f.machine(t, id)
		if machine.Online == online {
			return machine
		}
		time.Sleep(20 * time.Millisecond)
	}
	machine := f.machine(t, id)
	t.Fatalf("machine online = %t, want %t", machine.Online, online)
	return liveMachine{}
}

func dialMachineConnection(t *testing.T, serverURL, machineCredential string) *websocket.Conn {
	t.Helper()
	connectionURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/machines/current/connection"
	conn, _, err := websocket.Dial(context.Background(), connectionURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + machineCredential}},
	})
	if err != nil {
		t.Fatalf("open second machine WebSocket: %v", err)
	}
	_, _, err = conn.Read(context.Background())
	if err != nil {
		_ = conn.CloseNow()
		t.Fatalf("read second connection hello: %v", err)
	}
	return conn
}

func assertNoConnectionSecret(t *testing.T, audit connectionAudit) {
	t.Helper()
	if len(audit.credentials) == 0 {
		t.Fatal("secret audit has no credential")
	}
	for _, value := range audit.credentials {
		if value == "" {
			t.Error("secret audit contains an empty credential")
			continue
		}
		if strings.Contains(audit.contents, value) {
			t.Error("credential appeared in captured terminal output, logs, Machines responses, or state.json")
		}
	}
}

func (f *connectedFixture) assertStartCall(t *testing.T) {
	t.Helper()
	for _, call := range f.runner.callsSnapshot() {
		if call.name == "systemctl" && isDaemonStart(call.args) {
			return
		}
	}
	t.Fatalf("fake service did not record a daemon start call: %s", formatBackgroundCalls(f.runner.callsSnapshot()))
}

func formatBackgroundCalls(calls []backgroundCall) string {
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, call.name+" "+strings.Join(call.args, " "))
	}
	return fmt.Sprint(parts)
}
