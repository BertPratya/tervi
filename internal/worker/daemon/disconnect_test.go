package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

func TestDisconnectConnected(t *testing.T) {
	closeSeen := make(chan error, 1)
	f := newDaemonFixture(t, true, 30, 60, func(conn *websocket.Conn) {
		_, _, err := conn.Read(context.Background())
		closeSeen <- err
	})
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case <-f.checks:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not connect")
	}
	waitFor(t, time.Second, func() bool { return d.Status().State == StateConnected }, "connected state")

	result := d.Disconnect(context.Background())
	if result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	select {
	case err := <-closeSeen:
		if code := websocket.CloseStatus(err); code != websocket.StatusCode(1000) {
			t.Errorf("close code = %d, want 1000 (error %v)", code, err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe the disconnect close")
	}
	if got := d.Status(); got.State != StateNotConnecting || got.StopReason != nil {
		t.Errorf("Status() = %+v, want not connecting without a reason", got)
	}
	if got, _, err := state.ReadState(f.dir); err != nil || got.Connect || got.StopReason != nil || !got.Confirmed {
		t.Errorf("state after disconnect = %+v, err %v", got, err)
	}
	select {
	case err := <-done:
		t.Fatalf("Run() ended after disconnect: %v", err)
	default:
	}
	if got := strings.Count(f.logs.String(), "○ Disconnected."); got != 1 {
		t.Errorf("disconnect log count = %d, logs %q", got, f.logs.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestDisconnectReconnecting(t *testing.T) {
	f := newDaemonFixture(t, true, 30, 60, nil)
	var tries atomic.Int32
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		tries.Add(1)
		return nil, nil, errors.New("server unavailable")
	}, Rand: func() float64 { return 0 }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return tries.Load() == 1 && d.Status().State == StateReconnecting }, "first connection attempt")
	if result := d.Disconnect(context.Background()); result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	time.Sleep(1700 * time.Millisecond)
	if got := tries.Load(); got != 1 {
		t.Errorf("connection attempts after disconnect = %d, want 1", got)
	}
	if got := d.Status().State; got != StateNotConnecting {
		t.Errorf("Status().State = %q, want %q", got, StateNotConnecting)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestDisconnectedAfterRestart(t *testing.T) {
	f := newDaemonFixture(t, true, 30, 60, nil)
	var tries atomic.Int32
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		tries.Add(1)
		return nil, nil, errors.New("server unavailable")
	}})
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return tries.Load() == 1 }, "first daemon attempt")
	if result := d.Disconnect(context.Background()); result.Result != ResultDisconnected {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if got := tries.Load(); got != 1 {
		t.Fatalf("first daemon attempts = %d, want 1", got)
	}
	second := f.newDaemon(t, Options{})
	secondCtx, stopSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Run(secondCtx) }()
	waitFor(t, time.Second, func() bool { return second.Status().State == StateNotConnecting }, "idle state after restart")
	select {
	case <-f.checks:
		t.Fatal("restarted daemon connected while connect is false")
	case <-time.After(100 * time.Millisecond):
	}
	if got := tries.Load(); got != 1 {
		t.Errorf("connection attempts after restart = %d, want 1", got)
	}
	stopSecond()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonDisconnectOutcomesLeaveStateUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      state.State
		result     string
		writeFails bool
	}{
		{name: "already disconnected", state: state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true}, result: ResultAlreadyDisconnected},
		{name: "rejected", state: state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, StopReason: stringPointer(state.StopReasonCredentialRejected)}, result: ResultRejected},
		{name: "revoked", state: state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, StopReason: stringPointer(state.StopReasonCredentialRevoked)}, result: ResultRevoked},
		{name: "not paired", state: state.State{MachineID: "machine-id", CredentialSaved: true}, result: ResultNotPaired},
		{name: "write failure", state: state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, Connect: true}, result: ResultFailed, writeFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.writeFails {
				dir = makeLongStateDirForWriteFailure(t)
			}
			if tc.writeFails {
				data := []byte(`{"machine_id":"machine-id","credential_saved":true,"confirmed":true,"connect":true,"stop_reason":null}`)
				if err := os.WriteFile(state.StatePath(dir), data, 0600); err != nil {
					t.Fatalf("write original state: %v", err)
				}
			} else if err := state.WriteState(dir, tc.state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(state.StatePath(dir))
			if err != nil {
				t.Fatal(err)
			}
			store := credential.NewStore(credential.NewMemoryBackend())
			if err := store.Save(credential.Entry{Server: "http://localhost:8080", Credential: secret.FromString("credential")}); err != nil {
				t.Fatal(err)
			}
			d := New(Options{StateDir: dir, Store: store})
			got := d.Disconnect(context.Background())
			if got.Result != tc.result {
				t.Errorf("Disconnect() = %+v, want result %q", got, tc.result)
			}
			if tc.writeFails && got.Path != state.StatePath(dir) {
				t.Errorf("Disconnect().Path = %q, want %q", got.Path, state.StatePath(dir))
			}
			after, err := os.ReadFile(state.StatePath(dir))
			if err != nil || string(after) != string(before) {
				t.Errorf("state changed: err=%v before=%q after=%q", err, before, after)
			}
		})
	}
}

func TestDaemonDisconnectUnreadable(t *testing.T) {
	t.Run("state", func(t *testing.T) {
		dir := t.TempDir()
		path := state.StatePath(dir)
		if err := os.WriteFile(path, []byte("not-json"), 0600); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f := newDaemonFixture(t, false, 30, 60, nil)
		d := New(Options{StateDir: dir, Store: f.store})
		got := d.Disconnect(context.Background())
		if got.Result != "unreadable" || got.Path != path {
			t.Errorf("Disconnect() = %+v, want unreadable path %q", got, path)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Errorf("state changed: err=%v before=%q after=%q", err, before, after)
		}
	})
	t.Run("credential", func(t *testing.T) {
		f := newDaemonFixture(t, true, 30, 60, nil)
		before, err := os.ReadFile(state.StatePath(f.dir))
		if err != nil {
			t.Fatal(err)
		}
		store := credential.NewStore(disconnectReadFailureBackend{})
		d := New(Options{StateDir: f.dir, Store: store})
		got := d.Disconnect(context.Background())
		if got.Result != "unreadable" || got.Path != credential.Path(f.dir) {
			t.Errorf("Disconnect() = %+v, want unreadable path %q", got, credential.Path(f.dir))
		}
		after, err := os.ReadFile(state.StatePath(f.dir))
		if err != nil || string(after) != string(before) {
			t.Errorf("state changed: err=%v before=%q after=%q", err, before, after)
		}
	})
}

func TestDisconnectDoesNotRestoreTryingStatusAfterDisconnect(t *testing.T) {
	dir := t.TempDir()
	if err := state.WriteState(dir, state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	backend := credential.NewMemoryBackend()
	store := credential.NewStore(backend)
	if err := store.Save(credential.Entry{Server: "http://localhost:8080", Credential: secret.FromString("credential")}); err != nil {
		t.Fatal(err)
	}
	blocked := &disconnectCredentialBackend{
		base: backend, firstReadStarted: make(chan struct{}), releaseFirstRead: make(chan struct{}),
	}
	dialStatus := make(chan Status, 1)
	var d *Daemon
	d = New(Options{
		StateDir: dir,
		Store:    credential.NewStore(blocked),
		Dial: func(ctx context.Context, _ string, _ *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			dialStatus <- d.Status()
			return nil, nil, ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case <-blocked.firstReadStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("connection attempt did not start reading credentials")
	}
	if got := d.Disconnect(context.Background()); got.Result != ResultDisconnected {
		cancel()
		t.Fatalf("Disconnect() = %+v, want disconnected", got)
	}
	d.mu.Lock()
	changed := d.changed
	d.mu.Unlock()
	close(blocked.releaseFirstRead)
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("cancelled connection attempt did not refresh its state")
	}
	select {
	case got := <-dialStatus:
		t.Errorf("cancelled connection attempt reached dial with status %+v", got)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

type disconnectCredentialBackend struct {
	base             *credential.MemoryBackend
	mu               sync.Mutex
	reads            int
	firstReadStarted chan struct{}
	releaseFirstRead chan struct{}
}

func (b *disconnectCredentialBackend) Get(user string) (string, error) {
	b.mu.Lock()
	b.reads++
	read := b.reads
	b.mu.Unlock()
	if read == 1 {
		close(b.firstReadStarted)
		<-b.releaseFirstRead
	}
	return b.base.Get(user)
}

func (b *disconnectCredentialBackend) Set(user, value string) error { return b.base.Set(user, value) }
func (b *disconnectCredentialBackend) Delete(user string) error     { return b.base.Delete(user) }

type disconnectReadFailureBackend struct{}

func (disconnectReadFailureBackend) Get(string) (string, error) { return "", errors.New("read failed") }
func (disconnectReadFailureBackend) Set(string, string) error   { return nil }
func (disconnectReadFailureBackend) Delete(string) error        { return nil }

func makeLongStateDirForWriteFailure(t *testing.T) string {
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

func stringPointer(value string) *string { return &value }

func TestDaemonDisconnectDuringConnect(t *testing.T) {
	f := newDaemonFixture(t, false, 30, 60, nil)
	var tries atomic.Int32
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		tries.Add(1)
		return nil, nil, errors.New("server unavailable")
	}})
	d.connectTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	connectDone := make(chan ConnectResult, 1)
	go func() { connectDone <- d.Connect(context.Background()) }()
	select {
	case result := <-connectDone:
		if result.Result != ResultStillTrying {
			t.Fatalf("Connect() = %+v, want still_trying", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Connect() did not finish")
	}
	if result := d.Disconnect(context.Background()); result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	triesAtDisconnect := tries.Load()
	time.Sleep(100 * time.Millisecond)
	if got := tries.Load(); got != triesAtDisconnect {
		t.Errorf("connection attempts after the waiting connect ended = %d, want %d", got, triesAtDisconnect)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectDuringAttempt(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	acceptedClose := make(chan int, 1)
	release := make(chan struct{})
	var accepted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		select {
		case <-request.Context().Done():
			close(requestCancelled)
			return
		case <-release:
		}
		if request.Context().Err() != nil {
			close(requestCancelled)
			return
		}
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		accepted.Add(1)
		if err := conn.Write(request.Context(), websocket.MessageText, mustJSON(protocol.Hello{Type: protocol.TypeHello, CheckIntervalSeconds: 30, TimeoutSeconds: 60})); err != nil {
			_ = conn.CloseNow()
			return
		}
		_, _, err = conn.Read(context.Background())
		acceptedClose <- int(websocket.CloseStatus(err))
	}))
	defer server.Close()
	f := newDaemonFixture(t, true, 30, 60, nil)
	if err := f.store.Save(credential.Entry{Server: server.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("connection attempt did not start")
	}
	if result := d.Disconnect(context.Background()); result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	cancelledWhileHeld := false
	select {
	case <-requestCancelled:
		cancelledWhileHeld = true
	case <-time.After(250 * time.Millisecond):
	}
	close(release)
	if !cancelledWhileHeld {
		select {
		case <-requestCancelled:
		case <-time.After(time.Second):
			select {
			case code := <-acceptedClose:
				if code != 1000 {
					t.Errorf("server observed close code %d, want 1000", code)
				}
			case <-time.After(time.Second):
				if accepted.Load() == 0 {
					t.Error("opening request was neither cancelled nor accepted")
				} else {
					t.Error("server did not receive the normal close")
				}
			}
		}
	}
	waitFor(t, time.Second, func() bool { return d.Status().State == StateNotConnecting }, "idle after cancelling attempt")
	if strings.Contains(f.logs.String(), "✓ Connected") {
		t.Errorf("daemon logged Connected after disconnect: %q", f.logs.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectLogLine(t *testing.T) {
	closeSeen := make(chan struct{}, 1)
	f := newDaemonFixture(t, true, 30, 60, func(conn *websocket.Conn) {
		_, _, _ = conn.Read(context.Background())
		closeSeen <- struct{}{}
	})
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case <-f.checks:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not connect")
	}
	waitFor(t, time.Second, func() bool { return d.Status().State == StateConnected }, "connected state")
	if result := d.Disconnect(context.Background()); result.Result != ResultDisconnected {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("server did not observe closed connection")
	}
	time.Sleep(50 * time.Millisecond)
	logs := f.logs.String()
	if strings.Count(logs, "○ Disconnected.") != 1 {
		t.Errorf("disconnect log count != 1: %q", logs)
	}
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	if lines[len(lines)-1] == "" || !strings.HasSuffix(lines[len(lines)-1], "○ Disconnected.") {
		t.Errorf("last log line = %q, want disconnected; all logs %q", lines[len(lines)-1], logs)
	}
	if strings.Contains(logs, "Connection lost") || strings.Contains(logs, "Reconnecting") {
		t.Errorf("lost or reconnecting log appeared after disconnect: %q", logs)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnectAfterDisconnect(t *testing.T) {
	f := newDaemonFixture(t, true, 30, 60, nil)
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	if result := d.Disconnect(context.Background()); result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	if result := d.Connect(context.Background()); result.Result != ResultConnected {
		t.Fatalf("Connect() = %+v, want connected", result)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
