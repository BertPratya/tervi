package daemon

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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

const daemonCredential = "worker-secret-that-must-never-appear"

type safeBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

func (b *safeBuffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

type daemonFixture struct {
	dir     string
	store   *credential.Store
	backend *credential.MemoryBackend
	logs    *safeBuffer
	server  *httptest.Server
	checks  chan *websocket.Conn
}

func newDaemonFixture(t *testing.T, connect bool, interval, timeout int, onConnect func(*websocket.Conn)) *daemonFixture {
	t.Helper()
	f := &daemonFixture{dir: t.TempDir(), backend: credential.NewMemoryBackend(), logs: &safeBuffer{}, checks: make(chan *websocket.Conn, 20)}
	f.store = credential.NewStore(f.backend)
	if err := f.store.Save(credential.Entry{Server: "http://placeholder", Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/machines/current/connection" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+daemonCredential {
			http.Error(w, "missing proof", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, mustJSON(protocol.Hello{Type: protocol.TypeHello, CheckIntervalSeconds: interval, TimeoutSeconds: timeout})); err != nil {
			_ = conn.CloseNow()
			return
		}
		select {
		case f.checks <- conn:
		default:
		}
		if onConnect != nil {
			onConnect(conn)
			return
		}
		for {
			_, message, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if messageType, ok := protocol.ReadType(message); ok && messageType == protocol.TypePing {
				if err := conn.Write(context.Background(), websocket.MessageText, mustJSON(protocol.Pong{Type: protocol.TypePong})); err != nil {
					return
				}
			}
		}
	}))
	// Update the store after its listener is available so every test can dial it.
	serverEntry := credential.Entry{Server: f.server.URL, Credential: secret.FromString(daemonCredential)}
	if err := f.store.Save(serverEntry); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(f.dir, state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, Connect: connect}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.server.Close)
	return f
}

func (f *daemonFixture) newDaemon(t *testing.T, opts Options) *Daemon {
	t.Helper()
	opts.StateDir, opts.Store, opts.Log = f.dir, f.store, f.logs
	return New(opts)
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func waitFor(t *testing.T, timeout time.Duration, predicate func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConnectsAndPings(t *testing.T) {
	type receivedPing struct {
		message []byte
		at      time.Time
	}
	pings := make(chan receivedPing, 4)
	f := newDaemonFixture(t, true, 1, 4, func(conn *websocket.Conn) {
		for {
			_, message, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			pings <- receivedPing{message: message, at: time.Now()}
			if messageType, ok := protocol.ReadType(message); ok && messageType == protocol.TypePing {
				if err := conn.Write(context.Background(), websocket.MessageText, mustJSON(protocol.Pong{Type: protocol.TypePong})); err != nil {
					return
				}
			}
		}
	})
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	<-f.checks
	ctxRead, cancelRead := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancelRead()
	var first receivedPing
	select {
	case first = <-pings:
	case <-ctxRead.Done():
		t.Fatal("timed out waiting for first ping")
	}
	if got, ok := protocol.ReadType(first.message); !ok || got != protocol.TypePing {
		t.Fatalf("first worker message = %s, want ping", first.message)
	}
	var second receivedPing
	select {
	case second = <-pings:
	case <-ctxRead.Done():
		t.Fatal("timed out waiting for second ping")
	}
	if got, ok := protocol.ReadType(second.message); !ok || got != protocol.TypePing {
		t.Fatalf("second worker message = %s, want ping", second.message)
	}
	if got := d.Status(); got.State != "connected" || got.Server != f.server.URL || got.ConnectedSince.IsZero() {
		t.Fatalf("Status() = %+v, want connected with server and time", got)
	} else if first.at.Sub(got.ConnectedSince) < 800*time.Millisecond || first.at.Sub(got.ConnectedSince) > 1500*time.Millisecond || second.at.Sub(first.at) < 800*time.Millisecond || second.at.Sub(first.at) > 1500*time.Millisecond {
		t.Fatalf("ping times = hello+%s and gap %s, want about 1 second each", first.at.Sub(got.ConnectedSince), second.at.Sub(first.at))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
}

func TestUsesServerTiming(t *testing.T) {
	for _, test := range []struct {
		interval int
		timeout  int
	}{
		{interval: 1, timeout: 3},
		{interval: 2, timeout: 5},
	} {
		t.Run(fmt.Sprintf("interval_%d_timeout_%d", test.interval, test.timeout), func(t *testing.T) {
			pings := make(chan time.Time, 3)
			f := newDaemonFixture(t, true, test.interval, test.timeout, func(conn *websocket.Conn) {
				for {
					_, message, err := conn.Read(context.Background())
					if err != nil {
						return
					}
					if messageType, ok := protocol.ReadType(message); ok && messageType == protocol.TypePing {
						select {
						case pings <- time.Now():
						default:
						}
					}
				}
			})
			d := f.newDaemon(t, Options{})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			first := receivePing(t, pings)
			second := receivePing(t, pings)
			gap := second.Sub(first)
			wantGap := time.Duration(test.interval) * time.Second
			if gap < wantGap-500*time.Millisecond || gap > wantGap+500*time.Millisecond {
				t.Errorf("ping gap = %s, want about %s from hello", gap, wantGap)
			}
			waitFor(t, time.Duration(test.timeout+1)*time.Second, func() bool {
				return strings.Contains(f.logs.String(), fmt.Sprintf("no answer from the server for %d seconds", test.timeout))
			}, "timeout based on hello; logs: "+f.logs.String())
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run() = %v", err)
			}
		})
	}
}

func receivePing(t *testing.T, pings <-chan time.Time) time.Time {
	t.Helper()
	select {
	case at := <-pings:
		return at
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for ping")
		return time.Time{}
	}
}

func TestServerCloseNoticedAtOnce(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
	for _, test := range []struct {
		name   string
		code   int
		reason string
	}{
		{name: "server stopping", code: protocol.CloseServerStopping, reason: "the server is stopping"},
		{name: "replaced", code: protocol.CloseReplaced, reason: "replaced by a newer connection"},
		{name: "other close", code: 4003, reason: "the server closed the connection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDaemonFixture(t, true, 10, 30, func(conn *websocket.Conn) {
				_ = conn.Close(websocket.StatusCode(test.code), "server close")
			})
			d := f.newDaemon(t, Options{Now: func() time.Time { return fixed }})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitFor(t, 750*time.Millisecond, func() bool { return strings.Contains(f.logs.String(), "Connection lost: "+test.reason) }, "server close log")
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run() = %v", err)
			}
			want := "03:04:05 ✓ Connected to " + f.server.URL + "\n03:04:05 ✗ Connection lost: " + test.reason + "\n"
			if got := f.logs.String(); got != want {
				t.Fatalf("log lines = %q, want %q", got, want)
			}
		})
	}
}

func TestServerSilenceTimesOut(t *testing.T) {
	closeSeen := make(chan websocket.StatusCode, 1)
	f := newDaemonFixture(t, true, 10, 1, func(conn *websocket.Conn) {
		_, _, err := conn.Read(context.Background())
		closeSeen <- websocket.CloseStatus(err)
	})
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case code := <-closeSeen:
		if code != websocket.StatusCode(protocol.CloseTimeout) {
			t.Fatalf("close code = %d, want %d", code, protocol.CloseTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe timeout close")
	}
	waitFor(t, time.Second, func() bool {
		return strings.Contains(f.logs.String(), "Connection lost: no answer from the server for 1 seconds")
	}, "timeout loss log")
	cancel()
	<-done
}

func TestReconnectWaits(t *testing.T) {
	bases := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, base := range bases {
		for _, random := range []float64{0, 0.999999} {
			got := reconnectWait(i+1, random)
			factor := got.Seconds() / base.Seconds()
			if factor < 0.8 || factor > 1.2 {
				t.Errorf("reconnectWait(%d, %v) = %s, outside 0.8–1.2 × %s", i+1, random, got, base)
			}
		}
	}
	if got := reconnectWait(1, 0); got != 800*time.Millisecond {
		t.Errorf("first wait with Rand=0 = %s, want 800ms", got)
	}
	if got := reconnectWait(2, 0); got != 1600*time.Millisecond {
		t.Errorf("wait before try 2 = %s, want 1.6s", got)
	}
	if got := reconnectWait(7, 0.5); got != 30*time.Second {
		t.Errorf("wait after cap = %s, want 30s", got)
	}

	t.Run("loop retries after loss and resets after hello", func(t *testing.T) {
		var serverConnections int
		f := newDaemonFixture(t, true, 10, 30, func(conn *websocket.Conn) {
			serverConnections++
			_ = conn.Close(websocket.StatusCode(protocol.CloseServerStopping), "stopping")
		})
		var dialCalls int
		dial := func(ctx context.Context, address string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			dialCalls++
			if dialCalls == 1 || dialCalls == 9 {
				return websocket.Dial(ctx, address, options)
			}
			return nil, nil, errors.New("temporary network failure")
		}
		randomValues := []float64{0, 0.999999, 0, 0.999999, 0, 0.999999, 0, 0.999999, 0}
		randomCalls := 0
		fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
		d := f.newDaemon(t, Options{Now: func() time.Time { return fixed }, Dial: dial, Rand: func() float64 {
			value := randomValues[randomCalls]
			randomCalls++
			return value
		}})
		waits := make(chan time.Duration, 9)
		waitNumber := 0
		d.waitOverride = func(duration time.Duration, ctx context.Context) bool {
			waits <- duration
			waitNumber++
			if waitNumber == 9 {
				<-ctx.Done()
				return false
			}
			return true
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()
		gotWaits := make([]time.Duration, 0, 9)
		for len(gotWaits) < 9 {
			select {
			case wait := <-waits:
				gotWaits = append(gotWaits, wait)
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatalf("wait sequence stopped at %v; logs: %q", gotWaits, f.logs.String())
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
		bases := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, time.Second}
		for index, wait := range gotWaits {
			factor := float64(wait) / float64(bases[index])
			if factor < 0.8 || factor > 1.2 {
				t.Errorf("wait %d = %s, outside 0.8–1.2 × %s", index+1, wait, bases[index])
			}
		}
		lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
		if len(lines) == 0 || !strings.Contains(lines[0], "✓ Connected to ") {
			t.Errorf("first try after start was announced as a reconnect: %q", f.logs.String())
		}
		for _, line := range []string{
			"✗ Connection lost: the server is stopping",
			"… Reconnecting (try 1)",
			"… Reconnecting (try 2)",
			"… Reconnecting (try 8)",
			"✗ Temporary network failure. Next try in 2 seconds",
			"Next try in 36 seconds",
		} {
			if !strings.Contains(f.logs.String(), line) {
				t.Errorf("logs do not contain %q: %q", line, f.logs.String())
			}
		}
		if serverConnections != 2 {
			t.Errorf("server accepted %d connections, want two successful hello cycles", serverConnections)
		}
	})
}

func TestStoreUnreadableIsFailedTry(t *testing.T) {
	for _, test := range []struct {
		name    string
		failed  string
		prepare func(*daemonFixture)
		repair  func(*daemonFixture) error
	}{
		{
			name:    "unreadable",
			failed:  "Can't read credential.json",
			prepare: func(f *daemonFixture) { f.backend.FailGet = true },
			repair:  func(f *daemonFixture) error { f.backend.FailGet = false; return nil },
		},
		{
			name:   "damaged",
			failed: "Credential.json is damaged",
			prepare: func(f *daemonFixture) {
				if err := f.backend.Set("worker", "broken"); err != nil {
					t.Fatal(err)
				}
			},
			repair: func(f *daemonFixture) error {
				return f.store.Save(credential.Entry{Server: f.server.URL, Credential: secret.FromString(daemonCredential)})
			},
		},
		{
			name:   "missing",
			failed: "Credential.json is missing",
			prepare: func(f *daemonFixture) {
				if err := f.backend.Delete("worker"); err != nil {
					t.Fatal(err)
				}
			},
			repair: func(f *daemonFixture) error {
				return f.store.Save(credential.Entry{Server: f.server.URL, Credential: secret.FromString(daemonCredential)})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDaemonFixture(t, true, 10, 30, nil)
			test.prepare(f)
			d := f.newDaemon(t, Options{Rand: func() float64 { return 0 }})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitFor(t, time.Second, func() bool { return strings.Contains(f.logs.String(), test.failed+". Next try in") }, "failed store try")
			if got, _, err := state.ReadState(f.dir); err != nil || !got.Connect {
				t.Fatalf("state after store failure = (%+v, %v), want connect true", got, err)
			}
			if err := test.repair(f); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 4*time.Second, func() bool { return d.Status().State == "connected" }, "retry using the recovered store entry")
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run() = %v", err)
			}
		})
	}
}

func TestStatusWhileTrying(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	called := make(chan struct{}, 1)
	d := f.newDaemon(t, Options{Dial: func(ctx context.Context, _ string, _ *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		called <- struct{}{}
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	<-called
	status := d.Status()
	if status.State != "reconnecting" || status.Try != 1 || status.NextTryInSeconds != 0 || status.LastError != "" {
		t.Fatalf("Status() during initial try = %+v", status)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
	t.Run("after earlier failure", func(t *testing.T) {
		f := newDaemonFixture(t, true, 10, 30, nil)
		var calls int
		secondCall := make(chan struct{}, 1)
		d := f.newDaemon(t, Options{Dial: func(ctx context.Context, address string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			calls++
			if calls == 1 {
				return nil, nil, errors.New("temporary network failure")
			}
			secondCall <- struct{}{}
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}})
		d.waitOverride = func(_ time.Duration, ctx context.Context) bool { return ctx.Err() == nil }
		runCtx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(runCtx) }()
		select {
		case <-secondCall:
		case <-time.After(time.Second):
			stop()
			t.Fatal("daemon did not make its second try")
		}
		status := d.Status()
		if status.State != "reconnecting" || status.Try != 2 || status.NextTryInSeconds != 0 || status.LastError != "temporary network failure" {
			t.Fatalf("Status() during second try = %+v, want last error from first try", status)
		}
		stop()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
	t.Run("status rounds like retry log after failure", func(t *testing.T) {
		f := newDaemonFixture(t, true, 10, 30, nil)
		var calls int
		secondWait := make(chan struct{})
		d := f.newDaemon(t, Options{
			Rand: func() float64 { return 0 },
			Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				calls++
				return nil, nil, errors.New("temporary network failure")
			},
		})
		waits := 0
		d.waitOverride = func(_ time.Duration, ctx context.Context) bool {
			waits++
			if waits == 2 {
				close(secondWait)
				<-ctx.Done()
				return false
			}
			return true
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()
		select {
		case <-secondWait:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("daemon did not reach the wait after its second failed try")
		}
		status := d.Status()
		if status.Try != 3 || status.NextTryInSeconds != 3 {
			t.Errorf("Status() during retry wait = %+v, want try 3 in 3 seconds", status)
		}
		if !strings.Contains(f.logs.String(), "Next try in 3 seconds") {
			t.Errorf("retry log did not round the same wait to 3 seconds: %q", f.logs.String())
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
	t.Run("lost timeout clears last error", func(t *testing.T) {
		f := newDaemonFixture(t, true, 10, 1, func(conn *websocket.Conn) {
			_, _, _ = conn.Read(context.Background())
		})
		waiting := make(chan struct{})
		d := f.newDaemon(t, Options{})
		d.waitOverride = func(_ time.Duration, ctx context.Context) bool {
			close(waiting)
			<-ctx.Done()
			return false
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()
		select {
		case <-waiting:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("daemon did not enter retry wait after timeout loss")
		}
		status := d.Status()
		if status.State != "reconnecting" || status.Try != 1 || status.LastError != "" {
			t.Fatalf("Status() after timeout loss = %+v, want no last error before a failed try", status)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
}

func TestRefreshStateKeepsLastServer(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	d := f.newDaemon(t, Options{})
	d.setTrying(1, f.server.URL, time.Now())
	d.refreshState()

	status := d.Status()
	if status.State != StateReconnecting || status.Server != f.server.URL {
		t.Fatalf("Status() = %+v, want reconnecting with server %q", status, f.server.URL)
	}
}

func TestStatusReconnectsWhenCredentialFileIsUnreadable(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	f.backend.FailGet = true
	d := f.newDaemon(t, Options{Rand: func() float64 { return 0 }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run() = %v", err)
		}
	}()

	waitFor(t, time.Second, func() bool {
		return d.Status().LastError == "can't read credential.json"
	}, "credential-file failure in daemon status")
	status := d.Status()
	if status.State != StateReconnecting || status.Server != "" || status.LastError != "can't read credential.json" {
		t.Fatalf("Status() = %+v, want reconnecting without a known server and with the credential-file error", status)
	}
}

func TestSecondsText(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{seconds: 1, want: "1 second"},
		{seconds: 2, want: "2 seconds"},
	} {
		if got := SecondsText(tc.seconds); got != tc.want {
			t.Errorf("SecondsText(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestErrorTexts(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		response *http.Response
		last     string
		log      string
	}{
		{name: "refused", err: &net.OpError{Err: syscall.ECONNREFUSED}, last: "connection refused", log: "Connection refused"},
		{name: "timeout", err: context.DeadlineExceeded, last: "no answer from the server", log: "No answer from the server"},
		{name: "http", response: &http.Response{StatusCode: 503}, last: "the server answered 503", log: "The server answered 503"},
		{name: "credential file unreadable", err: fmt.Errorf("read credential file: %w", credential.ErrStoreUnreadable), last: "can't read credential.json", log: "Can't read credential.json"},
		{name: "credential file damaged", err: credential.ErrDamaged, last: "credential.json is damaged", log: "Credential.json is damaged"},
		{name: "missing credential file", err: credential.ErrNotFound, last: "credential.json is missing", log: "Credential.json is missing"},
		{name: "unprotected credential file takes precedence", err: errors.Join(credential.ErrUnprotected, credential.ErrStoreUnreadable), last: "other users may be able to read credential.json", log: "Other users may be able to read credential.json"},
		{name: "state unreadable", err: fmt.Errorf("read state.json: %w: malformed json", credential.ErrStateUnreadable), last: "can't read state.json", log: ""},
		{name: "network", err: errors.New("Network is down"), last: "network is down", log: "Network is down"},
	} {
		t.Run(test.name, func(t *testing.T) {
			last, logged := errorText(test.err, test.response, daemonCredential)
			if last != test.last || logged != test.log {
				t.Fatalf("errorText() = (%q, %q), want (%q, %q)", last, logged, test.last, test.log)
			}
		})
	}
}

func TestUnprotectedCredentialIsFailedTry(t *testing.T) {
	dir := t.TempDir()
	logs := &safeBuffer{}
	releaseRetry := make(chan struct{})
	waitingForRetry := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, mustJSON(protocol.Hello{Type: protocol.TypeHello, CheckIntervalSeconds: 10, TimeoutSeconds: 30})); err != nil {
			_ = conn.CloseNow()
			return
		}
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	store := credential.NewStore(credential.NewFileBackend(dir))
	if err := store.Save(credential.Entry{Server: server.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(dir, state.State{MachineID: "machine", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	path := credential.Path(dir)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	d := New(Options{StateDir: dir, Store: store, Log: logs, Rand: func() float64 { return 0 }})
	d.waitOverride = func(_ time.Duration, ctx context.Context) bool {
		close(waitingForRetry)
		select {
		case <-releaseRetry:
			return true
		case <-ctx.Done():
			return false
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = d.Run(ctx); close(done) }()
	waitFor(t, time.Second, func() bool { return d.Status().LastError == "other users may be able to read credential.json" }, "unprotected credential failure")
	select {
	case <-waitingForRetry:
	case <-time.After(time.Second):
		t.Fatal("daemon did not wait before retrying")
	}
	if status := d.Status(); status.State != StateReconnecting || status.Try != 2 || status.LastError != "other users may be able to read credential.json" {
		t.Errorf("Status() after refused read = %+v, want failed retry with connect enabled", status)
	}
	if record, exists, err := state.ReadState(dir); err != nil || !exists || !record.Connect {
		t.Errorf("state after refused read = (%+v, %v, %v), want Connect true", record, exists, err)
	}
	if !strings.Contains(logs.String(), "✗ Other users may be able to read credential.json. Next try in 2 seconds") {
		t.Errorf("retry log = %q, want unprotected credential failure", logs.String())
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	close(releaseRetry)
	waitFor(t, time.Second, func() bool { return d.Status().State == StateConnected }, "connection after credential file was made private")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("daemon did not stop")
	}
}

func TestKeepsTryingForever(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	calls := 0
	var mu sync.Mutex
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil, errors.New("temporary network failure")
	}})
	d.waitOverride = func(_ time.Duration, ctx context.Context) bool {
		timer := time.NewTimer(time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = d.Run(ctx); close(done) }()
	waitFor(t, time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return calls >= 20 }, "many failed connection tries")
	if got, _, err := state.ReadState(f.dir); err != nil || !got.Connect {
		t.Fatalf("state after repeated errors = (%+v, %v), want connect true", got, err)
	}
	cancel()
	<-done
}

func TestOnlyUnknownCredentialStops(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound, http.StatusUnauthorized} {
		response := &http.Response{StatusCode: status, Body: http.NoBody}
		if unknownCredential(response) {
			t.Errorf("unknownCredential(%d) = true, want false without exact body", status)
		}
	}
	response := &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}
	if unknownCredential(response) {
		t.Fatal("401 without unknown_credential body was treated as rejection")
	}
	if !isUnknownCredentialBody([]byte(`{"error":"unknown_credential"}`)) {
		t.Fatal("exact rejection body was not recognized")
	}
	if !isUnknownCredentialBody([]byte("{ \"error\": \"unknown_credential\" }\n")) {
		t.Fatal("JSON whitespace around the rejection was not recognized")
	}
	for _, body := range [][]byte{[]byte(`{"error":"other"}`), []byte(`not json`), []byte(`{"error":"unknown_credential","extra":true}`), []byte(`{"error":"other","error":"unknown_credential"}`)} {
		if isUnknownCredentialBody(body) {
			t.Errorf("body %q was treated as exact rejection", body)
		}
	}

	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "500", status: http.StatusInternalServerError, body: "server error\n"},
		{name: "404", status: http.StatusNotFound, body: "not found\n"},
		{name: "other 401", status: http.StatusUnauthorized, body: "{\"error\":\"other\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDaemonFixture(t, true, 10, 30, nil)
			var calls int
			var callsMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callsMu.Lock()
				calls++
				call := calls
				callsMu.Unlock()
				if call == 1 {
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				if err := conn.Write(r.Context(), websocket.MessageText, mustJSON(protocol.Hello{Type: protocol.TypeHello, CheckIntervalSeconds: 10, TimeoutSeconds: 30})); err != nil {
					_ = conn.CloseNow()
					return
				}
				_, _, _ = conn.Read(context.Background())
			}))
			defer server.Close()
			if err := f.store.Save(credential.Entry{Server: server.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
				t.Fatal(err)
			}
			d := f.newDaemon(t, Options{Rand: func() float64 { return 0 }})
			d.waitOverride = func(_ time.Duration, ctx context.Context) bool { return ctx.Err() == nil }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitFor(t, time.Second, func() bool { return d.Status().State == "connected" }, "retry after non-rejection failure")
			callsMu.Lock()
			gotCalls := calls
			callsMu.Unlock()
			if gotCalls < 2 {
				t.Fatalf("server received %d dials, want retry", gotCalls)
			}
			if got, _, err := state.ReadState(f.dir); err != nil || !got.Connect {
				t.Fatalf("state after %s = (%+v, %v), want connect true", test.name, got, err)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run() = %v", err)
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "refused", err: &net.OpError{Err: syscall.ECONNREFUSED}},
		{name: "timeout", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDaemonFixture(t, true, 10, 30, nil)
			calls := 0
			d := f.newDaemon(t, Options{Dial: func(ctx context.Context, address string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				calls++
				if calls == 1 {
					return nil, nil, test.err
				}
				return websocket.Dial(ctx, address, options)
			}})
			d.waitOverride = func(_ time.Duration, ctx context.Context) bool { return ctx.Err() == nil }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitFor(t, time.Second, func() bool { return d.Status().State == "connected" }, "retry after network failure")
			if calls < 2 {
				t.Fatalf("Dial called %d times, want retry", calls)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run() = %v", err)
			}
		})
	}
}

func TestRejectionStops(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
	f := newDaemonFixture(t, true, 10, 30, nil)
	rejectionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "{\"error\":\"unknown_credential\"}\n")
	}))
	defer rejectionServer.Close()
	if err := f.store.Save(credential.Entry{Server: rejectionServer.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{Now: func() time.Time { return fixed }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool { s := d.Status(); return s.State == "not_connecting" && s.StopReason != nil }, "credential rejection")
	if got, _, err := state.ReadState(f.dir); err != nil || got.Connect || got.StopReason == nil || *got.StopReason != "credential_rejected" {
		t.Fatalf("state after rejection = (%+v, %v)", got, err)
	}
	wantLog := "03:04:05 ✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server " + rejectionServer.URL + "\n"
	if got := f.logs.String(); got != wantLog {
		t.Fatalf("rejection log = %q, want %q", got, wantLog)
	}
	cancel()
	<-done
}

func TestDaemonStopsOnRevokedAnswer(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
	f := newDaemonFixture(t, true, 10, 30, nil)
	var calls atomic.Int32
	revokedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"credential_revoked"}`)
	}))
	defer revokedServer.Close()
	if err := f.store.Save(credential.Entry{Server: revokedServer.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(f.dir, state.State{MachineID: "preserved-machine", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{Now: func() time.Time { return fixed }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == StateNotConnecting && status.StopReason != nil && *status.StopReason == "credential_revoked"
	}, "revoked credential stop")
	record, _, err := state.ReadState(f.dir)
	if err != nil || record.Connect || record.StopReason == nil || *record.StopReason != "credential_revoked" || record.MachineID != "preserved-machine" || !record.CredentialSaved || !record.Confirmed {
		t.Fatalf("state after revocation = (%+v, %v), want disabled with reason and pairing fields preserved", record, err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("server received %d connection attempts after revocation, want 1", got)
	}
	wantLog := "03:04:05 ✗ This computer was unpaired on the server. Pair it again with: tervi pair --server " + revokedServer.URL + "\n"
	if got := f.logs.String(); got != wantLog {
		t.Errorf("revocation log = %q, want exactly %q", got, wantLog)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestDaemonStopsOnCloseRevoked(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, func(conn *websocket.Conn) {
		_ = conn.Close(websocket.StatusCode(protocol.CloseRevoked), "credential revoked")
	})
	var dials atomic.Int32
	d := f.newDaemon(t, Options{Dial: func(ctx context.Context, address string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		dials.Add(1)
		return websocket.Dial(ctx, address, options)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == StateNotConnecting && status.StopReason != nil && *status.StopReason == "credential_revoked"
	}, "revoked close stop")
	if record, _, err := state.ReadState(f.dir); err != nil || record.Connect || record.StopReason == nil || *record.StopReason != "credential_revoked" {
		t.Fatalf("state after revoked close = (%+v, %v), want connect false and credential_revoked", record, err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := dials.Load(); got != 1 {
		t.Fatalf("daemon made %d dials after a revoked close, want 1", got)
	}
	lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "✗ This computer was unpaired on the server. Pair it again with: tervi pair --server "+f.server.URL) || strings.Contains(lines[1], "Connection lost") {
		t.Fatalf("logs after revoked close = %q, want connected then the unpaired line", f.logs.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestDaemonStopsOnCloseRevokedBeforeHello(t *testing.T) {
	dir := t.TempDir()
	backend := credential.NewMemoryBackend()
	store := credential.NewStore(backend)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		requests.Add(1)
		_ = conn.Close(websocket.StatusCode(protocol.CloseRevoked), "credential revoked")
	}))
	defer server.Close()
	if err := store.Save(credential.Entry{Server: server.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(dir, state.State{MachineID: "machine-id", CredentialSaved: true, Confirmed: true, Connect: true}); err != nil {
		t.Fatal(err)
	}
	logs := &safeBuffer{}
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
	d := New(Options{StateDir: dir, Store: store, Log: logs, Now: func() time.Time { return fixed }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == StateNotConnecting && status.StopReason != nil && *status.StopReason == state.StopReasonCredentialRevoked
	}, "revoked close before hello")
	time.Sleep(100 * time.Millisecond)
	if got := requests.Load(); got != 1 {
		t.Errorf("server accepted %d connections after revoked close, want 1", got)
	}
	wantLog := "03:04:05 ✗ This computer was unpaired on the server. Pair it again with: tervi pair --server " + server.URL + "\n"
	if got := logs.String(); got != wantLog {
		t.Errorf("logs before hello = %q, want %q", got, wantLog)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestDaemonRevokedBodyStrict(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	var calls atomic.Int32
	revokedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		if call == 1 {
			_, _ = io.WriteString(w, `{"error":"credential_revoked","extra":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"error":"unknown_credential"}`)
	}))
	defer revokedServer.Close()
	if err := f.store.Save(credential.Entry{Server: revokedServer.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{Rand: func() float64 { return 0 }})
	d.waitOverride = func(_ time.Duration, ctx context.Context) bool { return ctx.Err() == nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == StateNotConnecting && status.StopReason != nil && *status.StopReason == state.StopReasonCredentialRejected
	}, "unknown credential rejection after unrecognized 401")
	if got := calls.Load(); got != 2 {
		t.Fatalf("server received %d attempts, want failed revoked-shaped response then unknown_credential rejection", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestConnectAfterRevoked(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	secondStarted := make(chan struct{})
	finishSecond := make(chan struct{})
	var calls atomic.Int32
	revokedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			close(secondStarted)
			<-finishSecond
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"credential_revoked"}`)
	}))
	defer revokedServer.Close()
	if err := f.store.Save(credential.Entry{Server: revokedServer.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == StateNotConnecting && status.StopReason != nil && *status.StopReason == "credential_revoked"
	}, "first revoked response")
	resultCh := make(chan ConnectResult, 1)
	go func() { resultCh <- d.Connect(ctx) }()
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Connect did not make a second dial")
	}
	between, _, err := state.ReadState(f.dir)
	if err != nil || !between.Connect || between.StopReason != nil {
		close(finishSecond)
		cancel()
		t.Fatalf("state during second connect attempt = (%+v, %v), want connect true and cleared reason", between, err)
	}
	close(finishSecond)
	select {
	case result := <-resultCh:
		if result.Result != "revoked" {
			t.Errorf("Connect() = %+v, want revoked", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Connect did not return after the second revoked response")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server received %d attempts, want 2", got)
	}
	if record, _, err := state.ReadState(f.dir); err != nil || record.Connect || record.StopReason == nil || *record.StopReason != state.StopReasonCredentialRevoked {
		t.Errorf("state after second revocation = (%+v, %v), want connect false and credential_revoked", record, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestRejectionPreservesCurrentPairingFields(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	expected := state.State{
		MachineID:       "new-machine-id",
		CredentialSaved: false,
		Confirmed:       false,
		Connect:         true,
	}
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		if err := state.WriteState(f.dir, expected); err != nil {
			t.Errorf("WriteState() while Dial is in progress: %v", err)
		}
		return nil, &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"error":"unknown_credential"}`)),
		}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == "not_connecting" && status.StopReason != nil
	}, "credential rejection")
	got, _, err := state.ReadState(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.MachineID != expected.MachineID || got.CredentialSaved != expected.CredentialSaved || got.Confirmed != expected.Confirmed {
		t.Fatalf("pairing fields after rejection = (%+v), want machine_id %q, credential_saved %t, confirmed %t", got, expected.MachineID, expected.CredentialSaved, expected.Confirmed)
	}
	if got.Connect || got.StopReason == nil || *got.StopReason != "credential_rejected" {
		t.Fatalf("connection fields after rejection = (%+v), want connect false and credential_rejected", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestConnectAfterRejection(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	secondDialStarted := make(chan struct{})
	finishSecondDial := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	rejectionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 2 {
			close(secondDialStarted)
			<-finishSecondDial
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "{\"error\":\"unknown_credential\"}\n")
	}))
	defer rejectionServer.Close()
	if err := f.store.Save(credential.Entry{Server: rejectionServer.URL, Credential: secret.FromString(daemonCredential)}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		status := d.Status()
		return status.State == "not_connecting" && status.StopReason != nil
	}, "first credential rejection")
	if got, _, err := state.ReadState(f.dir); err != nil || got.Connect || got.StopReason == nil || *got.StopReason != "credential_rejected" {
		t.Fatalf("state after first rejection = (%+v, %v)", got, err)
	}
	resultCh := make(chan ConnectResult, 1)
	go func() { resultCh <- d.Connect(ctx) }()
	select {
	case <-secondDialStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Connect did not make the second dial")
	}
	between, _, err := state.ReadState(f.dir)
	if err != nil || !between.Connect || between.StopReason != nil {
		close(finishSecondDial)
		cancel()
		t.Fatalf("state between rejections = (%+v, %v), want connect true and no stop reason", between, err)
	}
	close(finishSecondDial)
	var result ConnectResult
	select {
	case result = <-resultCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Connect did not return after the second rejection")
	}
	if result.Result != "rejected" {
		t.Fatalf("Connect() = %+v, want rejected", result)
	}
	if got, _, err := state.ReadState(f.dir); err != nil || got.Connect || got.StopReason == nil || *got.StopReason != "credential_rejected" {
		t.Fatalf("state after second rejection = (%+v, %v)", got, err)
	}
	callsMu.Lock()
	gotCalls := calls
	callsMu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("server received %d dials, want two rejections", gotCalls)
	}
	if got := strings.Count(f.logs.String(), "doesn't accept this computer's credential"); got != 2 {
		t.Fatalf("rejection log appeared %d times, want twice: %q", got, f.logs.String())
	}
	cancel()
	<-done
}

func TestIdleWhileConnectFalse(t *testing.T) {
	f := newDaemonFixture(t, false, 10, 30, nil)
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-f.checks:
		t.Fatal("daemon connected while connect was false")
	default:
	}
	if got := d.Status(); got.State != "not_connecting" {
		t.Fatalf("Status() = %+v, want not_connecting", got)
	}
	cancel()
	<-done
}

func TestDaemonConnectResults(t *testing.T) {
	if got := New(Options{}).connectTimeout; got != 30*time.Second {
		t.Fatalf("New().connectTimeout = %s, want 30s", got)
	}
	t.Run("not paired", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		if err := state.WriteState(f.dir, state.State{}); err != nil {
			t.Fatal(err)
		}
		d := f.newDaemon(t, Options{})
		if got := d.Connect(context.Background()); got.Result != "not_paired" {
			t.Fatalf("Connect() = %+v", got)
		}
	})
	t.Run("already connected", func(t *testing.T) {
		f := newDaemonFixture(t, true, 10, 30, nil)
		d := f.newDaemon(t, Options{})
		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx)
		waitFor(t, time.Second, func() bool { return d.Status().State == "connected" }, "connected status")
		if got := d.Connect(ctx); got.Result != "already_connected" {
			t.Fatalf("Connect() = %+v", got)
		}
		cancel()
	})
	t.Run("connected", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		d := f.newDaemon(t, Options{})
		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx)
		if got := d.Connect(ctx); got.Result != "connected" {
			t.Fatalf("Connect() = %+v", got)
		}
		cancel()
	})
	t.Run("still trying on cancellation", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		d := f.newDaemon(t, Options{Dial: func(ctx context.Context, _ string, _ *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}})
		runCtx, stop := context.WithCancel(context.Background())
		go d.Run(runCtx)
		connectCtx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(30 * time.Millisecond); cancel() }()
		if got := d.Connect(connectCtx); got.Result != "still_trying" || got.LastError != "" {
			t.Fatalf("Connect() = %+v", got)
		}
		stop()
	})
	t.Run("cancelled while waiting for connect lock", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		d := f.newDaemon(t, Options{})
		d.connectMu.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		result := make(chan ConnectResult, 1)
		go func() {
			close(started)
			result <- d.Connect(ctx)
		}()
		<-started
		cancel()
		d.connectMu.Unlock()
		if got := <-result; got.Result != "still_trying" {
			t.Fatalf("Connect() = %+v, want still_trying", got)
		}
		if record, _, err := state.ReadState(f.dir); err != nil || record.Connect {
			t.Fatalf("state after cancelled Connect = (%+v, %v), want connect false", record, err)
		}
		if d.isEnabled() {
			t.Fatal("cancelled Connect enabled connection attempts")
		}
	})
	t.Run("still trying after deadline", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			return nil, nil, errors.New("temporary network failure")
		}})
		d.connectTimeout = 40 * time.Millisecond
		runCtx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(runCtx) }()
		got := d.Connect(context.Background())
		if got.Result != "still_trying" || got.LastError != "temporary network failure" {
			t.Fatalf("Connect() = %+v, want still_trying with last error", got)
		}
		if record, _, err := state.ReadState(f.dir); err != nil || !record.Connect {
			t.Fatalf("state after connect deadline = (%+v, %v), want connect true", record, err)
		}
		stop()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
	t.Run("unreadable state preserved", func(t *testing.T) {
		f := newDaemonFixture(t, false, 10, 30, nil)
		before := []byte("{ malformed state that must stay byte-for-byte unchanged\n")
		if err := os.WriteFile(state.StatePath(f.dir), before, 0600); err != nil {
			t.Fatal(err)
		}
		d := f.newDaemon(t, Options{})
		if got := d.Connect(context.Background()); got.Result != "still_trying" || got.LastError != "can't read state.json" {
			t.Fatalf("Connect() = %+v", got)
		}
		after, err := os.ReadFile(state.StatePath(f.dir))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("unreadable state changed from %q to %q: %v", before, after, err)
		}
	})
	t.Run("state write failure", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("permission checks are bypassed for the root user")
		}
		f := newDaemonFixture(t, false, 10, 30, nil)
		before, err := os.ReadFile(state.StatePath(f.dir))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(f.dir, 0700) })
		var dials int
		d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			dials++
			return nil, nil, errors.New("unexpected dial")
		}})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()
		result := d.Connect(ctx)
		if result.Result != "failed" || result.Path != state.StatePath(f.dir) {
			t.Fatalf("Connect() = %+v, want failed with state path %q", result, state.StatePath(f.dir))
		}
		after, err := os.ReadFile(state.StatePath(f.dir))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("state changed after failed Connect write: before %q, after %q, err %v", before, after, err)
		}
		if got := d.Status(); got.State != "not_connecting" {
			t.Fatalf("Status() after failed Connect write = %+v, want unchanged not_connecting", got)
		}
		select {
		case <-f.checks:
			t.Fatal("Connect woke the loop after state write failed")
		default:
		}
		if dials != 0 {
			t.Fatalf("Dial called %d times after state write failed", dials)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
}

func TestUnreadableStateStaysIdle(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	before := []byte("{")
	if err := os.WriteFile(state.StatePath(f.dir), before, 0600); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	if got := d.Status(); got.State != "not_connecting" || got.StopReason != nil {
		t.Fatalf("Status() = %+v", got)
	}
	select {
	case <-f.checks:
		t.Fatal("daemon dialed with malformed state")
	default:
	}
	if strings.Contains(f.logs.String(), "✗") {
		t.Fatalf("unexpected log: %q", f.logs.String())
	}
	after, err := os.ReadFile(state.StatePath(f.dir))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("unreadable state changed from %q to %q: %v", before, after, err)
	}
	cancel()
	<-done
}

func TestConnectReadsNewPairing(t *testing.T) {
	f := newDaemonFixture(t, false, 10, 30, nil)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-credential" {
			http.Error(w, "bad proof", 401)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, mustJSON(protocol.Hello{Type: protocol.TypeHello, CheckIntervalSeconds: 10, TimeoutSeconds: 30}))
		_, _, _ = conn.Read(context.Background())
	}))
	defer other.Close()
	if err := f.store.Save(credential.Entry{Server: other.URL, Credential: secret.FromString("new-credential")}); err != nil {
		t.Fatal(err)
	}
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	if got := d.Connect(ctx); got.Result != "connected" {
		t.Fatalf("Connect() = %+v", got)
	}
	if got := d.Status(); got.Server != other.URL {
		t.Fatalf("server = %q, want new pairing %q", got.Server, other.URL)
	}
	cancel()
}

func TestLogLines(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 7*60*60))
	f := newDaemonFixture(t, true, 10, 30, nil)
	d := f.newDaemon(t, Options{Now: func() time.Time { return fixed }})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	waitFor(t, time.Second, func() bool { return strings.Contains(f.logs.String(), "✓ Connected to ") }, "connected log")
	cancel()
	text := f.logs.String()
	want := "03:04:05 ✓ Connected to " + f.server.URL + "\n"
	if text != want {
		t.Fatalf("log = %q, want %q", text, want)
	}
	if strings.Contains(text, "ping") || strings.Contains(text, "pong") || strings.Contains(text, "hello") {
		t.Fatalf("regular checks were logged: %q", text)
	}
	t.Run("failed try", func(t *testing.T) {
		f := newDaemonFixture(t, true, 10, 30, nil)
		d := f.newDaemon(t, Options{
			Now:  func() time.Time { return fixed },
			Rand: func() float64 { return 0 },
			Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				return nil, nil, &net.OpError{Err: syscall.ECONNREFUSED}
			},
		})
		d.waitOverride = func(_ time.Duration, ctx context.Context) bool { <-ctx.Done(); return false }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()
		waitFor(t, time.Second, func() bool { return strings.Contains(f.logs.String(), "Next try in 2 seconds") }, "failed try log")
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}
		want := "03:04:05 ✗ Connection refused. Next try in 2 seconds\n"
		if got := f.logs.String(); got != want {
			t.Fatalf("failed try log = %q, want %q", got, want)
		}
	})
}

func TestCredentialNeverInLogOrState(t *testing.T) {
	f := newDaemonFixture(t, true, 10, 30, nil)
	d := f.newDaemon(t, Options{Dial: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, nil, fmt.Errorf("server failed while using %s", daemonCredential)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	waitFor(t, 3*time.Second, func() bool { return strings.Contains(f.logs.String(), "Next try in") }, "failed try log")
	cancel()
	if strings.Contains(f.logs.String(), daemonCredential) {
		t.Fatalf("log contains credential: %q", f.logs.String())
	}
	if strings.Contains(d.Status().LastError, daemonCredential) {
		t.Fatalf("Status() contains credential: %+v", d.Status())
	}
	data, err := os.ReadFile(state.StatePath(f.dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), daemonCredential) {
		t.Fatalf("state contains credential: %q", data)
	}
}

func TestStopsCleanlyOnContextEnd(t *testing.T) {
	closeSeen := make(chan error, 1)
	f := newDaemonFixture(t, true, 10, 30, func(conn *websocket.Conn) {
		_, _, err := conn.Read(context.Background())
		closeSeen <- err
	})
	d := f.newDaemon(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	<-f.checks
	waitFor(t, time.Second, func() bool { return d.Status().State == "connected" }, "connected status before shutdown")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	select {
	case err := <-closeSeen:
		code := websocket.CloseStatus(err)
		if code != websocket.StatusCode(protocol.CloseNormal) {
			t.Fatalf("close = %d (error %v), want 1000", code, err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe clean close")
	}
}

func ioNopCloser(value string) io.ReadCloser { return io.NopCloser(strings.NewReader(value)) }
