package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return tries.Load() == 1 && d.Status().State == StateReconnecting }, "first connection attempt")
	if result := d.Disconnect(context.Background()); result.Result != "disconnected" {
		t.Fatalf("Disconnect() = %+v, want disconnected", result)
	}
	time.Sleep(100 * time.Millisecond)
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
	d := f.newDaemon(t, Options{})
	if err := state.WriteState(f.dir, state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- d.Run(ctx) }()
	if result := d.Disconnect(context.Background()); result.Result != "already_disconnected" {
		t.Fatalf("Disconnect() = %+v, want already_disconnected", result)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
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
	stopSecond()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

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
