package daemon_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

func TestDisconnectDuringConnect(t *testing.T) {
	stateDir := t.TempDir()
	backend := credential.NewMemoryBackend()
	store := credential.NewStore(backend)
	if err := store.Save(credential.Entry{Server: "http://unreachable.example", Credential: secret.FromString("credential")}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(stateDir, state.State{MachineID: "machine", CredentialSaved: true, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	engine := daemon.New(daemon.Options{
		StateDir: stateDir,
		Store:    store,
		Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			attempts.Add(1)
			return nil, nil, errors.New("unreachable")
		},
	})
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	ctx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- control.Run(ctx, runtimeDir, engine) }()
	finished := false
	t.Cleanup(func() {
		stop()
		if !finished {
			<-runDone
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := (control.Client{RuntimeDir: runtimeDir}).Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("daemon socket did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 250*time.Millisecond)
	t.Cleanup(cancelConnect)
	connectDone := make(chan error, 1)
	go func() {
		_, err := (control.Client{RuntimeDir: runtimeDir}).Connect(connectCtx)
		connectDone <- err
	}()
	waitForDial := time.Now().Add(time.Second)
	for attempts.Load() == 0 && time.Now().Before(waitForDial) {
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() == 0 {
		cancelConnect()
		stop()
		t.Fatal("POST /connect did not start a connection attempt")
	}
	disconnectDone := make(chan struct {
		answer control.DisconnectAnswer
		err    error
	}, 1)
	go func() {
		answer, err := (control.Client{RuntimeDir: runtimeDir}).Disconnect(context.Background())
		disconnectDone <- struct {
			answer control.DisconnectAnswer
			err    error
		}{answer, err}
	}()
	select {
	case err := <-connectDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("POST /connect error = %v, want its waiting context to expire", err)
		}
	case <-time.After(time.Second):
		t.Fatal("POST /connect did not finish after its context expired")
	}
	cancelConnect()
	select {
	case result := <-disconnectDone:
		if result.err != nil || result.answer.Result != daemon.ResultDisconnected {
			t.Fatalf("POST /disconnect = %+v, %v", result.answer, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("POST /disconnect did not finish after POST /connect released the lock")
	}
	triesAtDisconnect := attempts.Load()
	time.Sleep(100 * time.Millisecond)
	if got := attempts.Load(); got != triesAtDisconnect {
		t.Errorf("connection attempts after POST /disconnect = %d, want %d", got, triesAtDisconnect)
	}
	if status := engine.Status(); status.State != daemon.StateNotConnecting {
		t.Errorf("daemon status = %+v, want not connecting", status)
	}
	stop()
	if err := <-runDone; err != nil {
		t.Fatalf("control.Run() = %v", err)
	}
	finished = true
}
