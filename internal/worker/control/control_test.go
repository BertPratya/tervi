package control

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/daemon"
)

type fakeEngine struct {
	status daemon.Status
	result daemon.ConnectResult
}

func (f *fakeEngine) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (f *fakeEngine) Status() daemon.Status { return f.status }

func (f *fakeEngine) Connect(context.Context) daemon.ConnectResult { return f.result }

func TestSecondDaemonExits(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	firstCtx, stopFirst := context.WithCancel(context.Background())
	firstDone := startDaemon(t, firstCtx, runtimeDir, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))

	err := Run(context.Background(), runtimeDir, &fakeEngine{})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run() error = %v, want ErrAlreadyRunning", err)
	}
	if _, err := (Client{RuntimeDir: runtimeDir}).Status(context.Background()); err != nil {
		t.Fatalf("first daemon stopped answering: %v", err)
	}

	stopFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

func TestLockReleasedWhenDaemonEnds(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	firstCtx, stopFirst := context.WithCancel(context.Background())
	firstDone := startDaemon(t, firstCtx, runtimeDir, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
	stopFirst()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	secondCtx, stopSecond := context.WithCancel(context.Background())
	secondDone := startDaemon(t, secondCtx, runtimeDir, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
	if _, err := (Client{RuntimeDir: runtimeDir}).Status(context.Background()); err != nil {
		t.Fatalf("new daemon did not answer after lock release: %v", err)
	}
	stopSecond()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestOldSocketFileReplaced(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(runtimeDir, "daemon.sock")
	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := startDaemon(t, ctx, runtimeDir, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := (Client{RuntimeDir: runtimeDir}).Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replacement socket did not answer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeFolderPrivate(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	path, err := RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := startDaemon(t, ctx, path, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	waitForSocket(t, filepath.Join(path, "daemon.sock"))
	for name, want := range map[string]os.FileMode{
		"runtime directory": 0700,
		"socket":            0600,
		"lock":              0600,
	} {
		file := path
		switch name {
		case "socket":
			file = filepath.Join(path, "daemon.sock")
		case "lock":
			file = filepath.Join(path, "daemon.lock")
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", name, got, want)
		}
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOtherUserRefused(t *testing.T) {
	if peerAllowed(101, 100) {
		t.Error("peer with a different uid was allowed")
	}
	if !peerAllowed(100, 100) {
		t.Error("peer with the daemon uid was refused")
	}

	runtimeDir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler(&fakeEngine{status: daemon.Status{State: "not_paired"}})}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(&sameUserListener{Listener: listener, uid: uint32(os.Getuid()) + 1})
	}()
	if _, err := rawRequest(t, runtimeDir, "GET", "/status"); err == nil {
		t.Fatal("request through listener with another uid was accepted")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request timed out instead of being refused: %v", err)
	}
	_ = server.Close()
	if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestStatusOverSocket(t *testing.T) {
	since := time.Date(2026, 10, 13, 12, 30, 0, 0, time.UTC)
	next := since.Add(4 * time.Second)
	cases := []struct {
		name   string
		status daemon.Status
		want   string
	}{
		{"connected", daemon.Status{State: "connected", Server: "https://example.test", ConnectedSince: since}, `{"state":"connected","server":"https://example.test","since":"2026-10-13T12:30:00Z"}`},
		{"reconnecting", daemon.Status{State: "reconnecting", Server: "https://example.test", Try: 3, NextTry: next, NextTryInSeconds: 4, LastError: "connection refused"}, `{"state":"reconnecting","server":"https://example.test","try":3,"next_try_in_seconds":4,"last_error":"connection refused"}`},
		{"reconnecting without error", daemon.Status{State: "reconnecting", Server: "https://example.test", Try: 1, NextTry: next, NextTryInSeconds: 4}, `{"state":"reconnecting","server":"https://example.test","try":1,"next_try_in_seconds":4,"last_error":null}`},
		{"not connecting", daemon.Status{State: "not_connecting"}, `{"state":"not_connecting","stop_reason":null}`},
		{"credential rejected", daemon.Status{State: "not_connecting", StopReason: stringPointer("credential_rejected")}, `{"state":"not_connecting","stop_reason":"credential_rejected"}`},
		{"not paired", daemon.Status{State: "not_paired"}, `{"state":"not_paired"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := filepath.Join(t.TempDir(), "tervi")
			ctx, stop := context.WithCancel(context.Background())
			done := startDaemon(t, ctx, runtimeDir, &fakeEngine{status: tc.status})
			defer func() { stop(); <-done }()
			waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
			got, err := rawRequest(t, runtimeDir, "GET", "/status")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("GET /status = %s, want %s", got, tc.want)
			}
			gotAnswer, err := (Client{RuntimeDir: runtimeDir}).Status(context.Background())
			if err != nil {
				t.Fatalf("Client.Status() error = %v", err)
			}
			wantAnswer := statusAnswer(tc.status)
			if !reflect.DeepEqual(gotAnswer, wantAnswer) {
				t.Errorf("Client.Status() = %+v, want %+v", gotAnswer, wantAnswer)
			}
		})
	}
}

func TestConnectOverSocket(t *testing.T) {
	cases := []struct {
		name   string
		result daemon.ConnectResult
		want   string
	}{
		{"connected", daemon.ConnectResult{Result: "connected"}, `{"result":"connected"}`},
		{"already connected", daemon.ConnectResult{Result: "already_connected"}, `{"result":"already_connected"}`},
		{"rejected", daemon.ConnectResult{Result: "rejected"}, `{"result":"rejected"}`},
		{"not paired", daemon.ConnectResult{Result: "not_paired"}, `{"result":"not_paired"}`},
		{"still trying", daemon.ConnectResult{Result: "still_trying", LastError: "connection refused"}, `{"result":"still_trying","last_error":"connection refused"}`},
		{"still trying without error", daemon.ConnectResult{Result: "still_trying"}, `{"result":"still_trying","last_error":null}`},
		{"failed", daemon.ConnectResult{Result: "failed", Path: "/state/state.json"}, `{"result":"failed","path":"/state/state.json"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := filepath.Join(t.TempDir(), "tervi")
			ctx, stop := context.WithCancel(context.Background())
			done := startDaemon(t, ctx, runtimeDir, &fakeEngine{result: tc.result})
			defer func() { stop(); <-done }()
			waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
			got, err := rawRequest(t, runtimeDir, "POST", "/connect")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("POST /connect = %s, want %s", got, tc.want)
			}
			gotAnswer, err := (Client{RuntimeDir: runtimeDir}).Connect(context.Background())
			if err != nil {
				t.Fatalf("Client.Connect() error = %v", err)
			}
			wantAnswer := connectAnswer(tc.result)
			if !reflect.DeepEqual(gotAnswer, wantAnswer) {
				t.Errorf("Client.Connect() = %+v, want %+v", gotAnswer, wantAnswer)
			}
		})
	}
}

func TestClientNotRunning(t *testing.T) {
	t.Run("missing socket", func(t *testing.T) {
		_, err := (Client{RuntimeDir: t.TempDir()}).Status(context.Background())
		if !errors.Is(err, ErrNotRunning) {
			t.Fatalf("Status() error = %v, want ErrNotRunning", err)
		}
	})
	t.Run("refused socket", func(t *testing.T) {
		dir := t.TempDir()
		listener, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
		if err != nil {
			t.Fatal(err)
		}
		_ = listener.Close()
		_, err = (Client{RuntimeDir: dir}).Status(context.Background())
		if !errors.Is(err, ErrNotRunning) {
			t.Fatalf("Status() error = %v, want ErrNotRunning", err)
		}
	})
	t.Run("no answer within five seconds", func(t *testing.T) {
		dir := t.TempDir()
		listener, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				defer conn.Close()
				buffer := make([]byte, 4096)
				_, _ = conn.Read(buffer)
				<-time.After(6 * time.Second)
			}
		}()
		started := time.Now()
		_, err = (Client{RuntimeDir: dir}).Status(context.Background())
		if !errors.Is(err, ErrNotRunning) {
			t.Fatalf("Status() error = %v, want ErrNotRunning", err)
		}
		if time.Since(started) > 5500*time.Millisecond {
			t.Errorf("Status() took %s, want at most 5.5s", time.Since(started))
		}
	})
}

func TestClientCancelAndSlowConnect(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(Client, context.Context) error
	}{
		{"status", func(client Client, ctx context.Context) error { _, err := client.Status(ctx); return err }},
		{"connect", func(client Client, ctx context.Context) error { _, err := client.Connect(ctx); return err }},
	} {
		t.Run("cancel "+tc.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			listener, err := net.Listen("unix", filepath.Join(runtimeDir, "daemon.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			accepted := make(chan struct{})
			serveDone := make(chan struct{})
			go func() {
				defer close(serveDone)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				close(accepted)
				<-ctx.Done()
			}()

			clientDone := make(chan error, 1)
			go func() { clientDone <- tc.call(Client{RuntimeDir: runtimeDir}, ctx) }()
			select {
			case <-accepted:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("client did not reach socket")
			}
			cancel()
			select {
			case err := <-clientDone:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("client error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("client did not return after cancellation")
			}
			<-serveDone
		})
	}
	t.Run("socket reached but no answer", func(t *testing.T) {
		runtimeDir := t.TempDir()
		listener, err := net.Listen("unix", filepath.Join(runtimeDir, "daemon.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()

		accepted := make(chan struct{})
		release := make(chan struct{})
		serveDone := make(chan struct{})
		go func() {
			defer close(serveDone)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			close(accepted)
			<-release
		}()

		oldTimeout := connectTimeout
		connectTimeout = 100 * time.Millisecond
		defer func() { connectTimeout = oldTimeout }()
		answer, err := (Client{RuntimeDir: runtimeDir}).Connect(context.Background())
		if err != nil {
			t.Fatalf("Connect() error = %v, want still_trying answer", err)
		}
		select {
		case <-accepted:
		default:
			t.Fatal("client returned before reaching the socket")
		}
		if answer.Result != "still_trying" || answer.LastError != nil {
			t.Errorf("Connect() answer = %+v, want still_trying with no last error", answer)
		}
		close(release)
		<-serveDone
	})
}

type delayedConnectEngine struct {
	started  chan struct{}
	finished chan struct{}
}

func (engine *delayedConnectEngine) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (engine *delayedConnectEngine) Status() daemon.Status {
	return daemon.Status{State: "not_paired"}
}

func (engine *delayedConnectEngine) Connect(ctx context.Context) daemon.ConnectResult {
	close(engine.started)
	<-ctx.Done()
	time.Sleep(150 * time.Millisecond)
	close(engine.finished)
	return daemon.ConnectResult{Result: "still_trying"}
}

func TestRunWaitsForHandlersOnShutdown(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	ctx, stop := context.WithCancel(context.Background())
	engine := &delayedConnectEngine{started: make(chan struct{}), finished: make(chan struct{})}
	done := startDaemon(t, ctx, runtimeDir, engine)
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
	requestDone := make(chan struct{})
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRequest()
	go func() {
		defer close(requestDone)
		_, _ = (Client{RuntimeDir: runtimeDir}).Connect(requestCtx)
	}()
	select {
	case <-engine.started:
	case <-time.After(2 * time.Second):
		stop()
		t.Fatal("connect handler did not start")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case <-engine.finished:
	default:
		t.Fatal("Run() returned while the connect handler was still running")
	}
	<-requestDone
}

type blockedConnectEngine struct {
	started chan struct{}
	release <-chan struct{}
}

func (engine *blockedConnectEngine) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (engine *blockedConnectEngine) Status() daemon.Status {
	return daemon.Status{State: "not_paired"}
}

func (engine *blockedConnectEngine) Connect(context.Context) daemon.ConnectResult {
	close(engine.started)
	<-engine.release
	return daemon.ConnectResult{Result: "still_trying"}
}

func TestRunBoundsHandlerWaitOnShutdown(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	ctx, stop := context.WithCancel(context.Background())
	releaseHandler := make(chan struct{})
	engine := &blockedConnectEngine{started: make(chan struct{}), release: releaseHandler}
	done := startDaemon(t, ctx, runtimeDir, engine)
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = (Client{RuntimeDir: runtimeDir}).Connect(context.Background())
	}()
	select {
	case <-engine.started:
	case <-time.After(2 * time.Second):
		stop()
		close(releaseHandler)
		t.Fatal("connect handler did not start")
	}

	started := time.Now()
	stop()
	var runErr error
	timedOut := false
	select {
	case runErr = <-done:
	case <-time.After(2500 * time.Millisecond):
		timedOut = true
		close(releaseHandler)
		runErr = <-done
	}
	if !timedOut {
		close(releaseHandler)
	}
	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if timedOut {
		t.Fatalf("Run() did not return within 2.5s; elapsed %s", time.Since(started))
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Error("client request did not finish after daemon shutdown")
	}
}

func TestUnexpectedEngineExitReason(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	err := Run(context.Background(), runtimeDir, &returningEngine{})
	if err == nil || err.Error() != "engine ended" {
		t.Fatalf("Run() error = %v, want engine ended", err)
	}
}

type returningEngine struct{}

func (returningEngine) Run(context.Context) error { return nil }
func (returningEngine) Status() daemon.Status     { return daemon.Status{State: "not_paired"} }
func (returningEngine) Connect(context.Context) daemon.ConnectResult {
	return daemon.ConnectResult{Result: "not_paired"}
}

func TestDaemonCommandStops(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	ctx, stop := context.WithCancel(context.Background())
	done := startDaemon(t, ctx, runtimeDir, &fakeEngine{status: daemon.Status{State: "not_paired"}})
	socket := filepath.Join(runtimeDir, "daemon.sock")
	waitForSocket(t, socket)
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket remains after daemon stopped: stat error = %v", err)
	}
}

func TestRuntimeDirRequiresEnvironment(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	_, err := RuntimeDir()
	if err == nil {
		t.Fatal("RuntimeDir() succeeded with empty XDG_RUNTIME_DIR")
	}
}

func startDaemon(t *testing.T, ctx context.Context, runtimeDir string, engine Engine) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, runtimeDir, engine) }()
	return done
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", path)
}

func rawRequest(t *testing.T, runtimeDir, method, path string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtimeDir, "daemon.sock"))
	}}
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer transport.CloseIdleConnections()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func stringPointer(value string) *string { return &value }
