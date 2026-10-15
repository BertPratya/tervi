package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testCredential = "connection-test-credential-do-not-log"
const testMachineID = "11111111-1111-4111-8111-111111111111"

type routeFixture struct {
	pool       *pgxpool.Pool
	list       *List
	logger     *slog.Logger
	logs       *bytes.Buffer
	server     *httptest.Server
	clock      *testClock
	afterCheck func(machineID string)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newRouteFixture(t *testing.T, interval, timeout time.Duration) *routeFixture {
	t.Helper()
	pool := dbtest.New(t)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	clock := &testClock{now: time.Now()}
	list := NewList()
	fixture := &routeFixture{pool: pool, list: list, logger: logger, logs: logs, clock: clock}
	mux := http.NewServeMux()
	Register(mux, Deps{Pool: pool, Logger: logger, List: list, CheckInterval: interval, ConnectionTimeout: timeout, Now: clock.Now,
		AfterCheck: func(machineID string) {
			if fixture.afterCheck != nil {
				fixture.afterCheck(machineID)
			}
		},
	})
	server := httptest.NewServer(mux)
	fixture.server = server
	fixture.addMachine(t, "active", testCredential)
	t.Cleanup(func() {
		list.CloseAll()
		server.Close()
		for _, credential := range []string{testCredential, "unknown-credential", "pending-credential", "failed-credential"} {
			if strings.Contains(logs.String(), credential) {
				t.Errorf("server log contains a credential")
			}
		}
	})
	return fixture
}

func (f *routeFixture) addMachine(t *testing.T, status, credential string) string {
	t.Helper()
	machineID := ""
	if credential == testCredential {
		machineID = testMachineID
	}
	gotMachineID, _ := dbtest.InsertMachineCredential(t, f.pool, dbtest.MachineCredential{
		MachineID: machineID, DisplayName: "test machine", Hash: secret.Hash(secret.FromString(credential)), Status: status,
	})
	return gotMachineID
}

func TestConnectionRefusesRevoked(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	f.addMachine(t, "revoked", "revoked-credential")
	f.addMachine(t, "expired", "expired-credential")
	if _, err := f.pool.Exec(context.Background(), `UPDATE credentials SET expires_at = now() - interval '1 second'
		WHERE credential_hash = $1`, secret.Hash(secret.FromString("expired-credential"))); err != nil {
		t.Fatalf("expire credential fixture: %v", err)
	}
	for _, test := range []struct {
		proof string
		want  string
	}{
		{proof: "revoked-credential", want: "credential_revoked"},
		{proof: "unknown-route-credential", want: "unknown_credential"},
		{proof: "pending-credential", want: "unknown_credential"},
		{proof: "failed-credential", want: "unknown_credential"},
		{proof: "expired-credential", want: "unknown_credential"},
	} {
		t.Run(test.proof, func(t *testing.T) {
			_, response, err := f.dial(t, test.proof, nil)
			if err == nil {
				t.Fatal("revoked or inactive credential opened a WebSocket")
			}
			assertJSONError(t, response, http.StatusUnauthorized, test.want)
		})
	}
}

func TestRevokedWhileOpening(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	f.afterCheck = func(machineID string) {
		if machineID != testMachineID {
			t.Errorf("AfterCheck machine ID = %q, want %q", machineID, testMachineID)
		}
		if _, err := f.pool.Exec(context.Background(), `UPDATE credentials SET status = 'revoked', ended_at = now()
			WHERE machine_id = $1`, machineID); err != nil {
			t.Errorf("revoke credential in AfterCheck: %v", err)
		}
	}
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("complete WebSocket handshake: %v", err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseRevoked {
		t.Fatalf("opening connection close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseRevoked)
	}
	if f.list.Online(testMachineID) {
		t.Fatal("machine remains Online after its credential was revoked during opening")
	}
}

func TestCloseRevoked(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	if !f.list.Online(testMachineID) {
		t.Fatal("machine is not Online after connecting")
	}
	f.list.CloseRevoked(testMachineID)
	if f.list.Online(testMachineID) {
		t.Fatal("machine remains Online immediately after CloseRevoked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseRevoked {
		t.Fatalf("connection close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseRevoked)
	}
	conn.CloseNow()
	f.list.CloseRevoked("machine-without-connection")
}

func TestCloseRevokedTrackedWhileStopping(t *testing.T) {
	list := NewList()
	fake := newForceCloseSocket()
	setConnection, finish := list.trackHandler()
	setConnection(fake)
	if !list.add("machine", fake) {
		t.Fatal("add() rejected the connection before shutdown")
	}
	list.CloseRevoked("machine")
	stopped := make(chan struct{})
	go func() {
		list.CloseAll()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("CloseAll returned while a revoked close was still pending")
	case <-time.After(100 * time.Millisecond):
	}
	_ = fake.CloseNow()
	finish()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("CloseAll did not return after revoked close finished")
	}
}

func (f *routeFixture) dial(t *testing.T, proof string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if headers == nil {
		headers = make(http.Header)
	}
	if proof != "" {
		headers.Set("Authorization", "Bearer "+proof)
	}
	return websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/machines/current/connection", &websocket.DialOptions{HTTPHeader: headers})
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read WebSocket message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode WebSocket message: %v", err)
	}
	return message
}

func assertJSONError(t *testing.T, response *http.Response, wantStatus int, wantError string) {
	t.Helper()
	if response == nil {
		t.Fatal("WebSocket dial returned no HTTP response")
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("HTTP status = %d, want %d; body = %q", response.StatusCode, wantStatus, body)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != wantError {
		t.Fatalf("error = %q, want %q", body["error"], wantError)
	}
}

func TestUnknownCredentialRefused(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	f.addMachine(t, "pending", "pending-credential")
	f.addMachine(t, "failed", "failed-credential")
	for _, test := range []struct {
		name   string
		proof  string
		status string
	}{
		{name: "missing"},
		{name: "unknown", proof: "unknown-credential"},
		{name: "pending", proof: "pending-credential"},
		{name: "failed", proof: "failed-credential"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, response, err := f.dial(t, test.proof, nil)
			if err == nil {
				t.Fatal("unknown or inactive credential opened a WebSocket")
			}
			assertJSONError(t, response, http.StatusUnauthorized, "unknown_credential")
		})
	}
}

func TestDatabaseFailureIsNotRejection(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	f.pool.Close()
	_, response, err := f.dial(t, testCredential, nil)
	if err == nil {
		t.Fatal("database failure opened a WebSocket")
	}
	assertJSONError(t, response, http.StatusInternalServerError, "internal_error")
	if got := f.logs.String(); !strings.Contains(got, `kind="look up machine"`) {
		t.Fatalf("log = %q, want operation kind", got)
	}
	if strings.Contains(f.logs.String(), testCredential) {
		t.Fatal("database failure log contains the credential")
	}
}

func TestLastSeenWriteFailureKeepsConnectionOpen(t *testing.T) {
	f := newRouteFixture(t, time.Second, 3*time.Second)
	if _, err := f.pool.Exec(context.Background(), `CREATE FUNCTION reject_connection_last_seen() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN
		RAISE EXCEPTION 'test database marker' USING ERRCODE = '40001';
		END $$`); err != nil {
		t.Fatalf("create last_seen_at trigger function: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(), `CREATE TRIGGER reject_connection_last_seen
		BEFORE UPDATE OF last_seen_at ON machines FOR EACH ROW
		EXECUTE FUNCTION reject_connection_last_seen()`); err != nil {
		t.Fatalf("create last_seen_at trigger: %v", err)
	}
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket despite last_seen_at failure: %v", err)
	}
	defer conn.CloseNow()
	if message := readJSON(t, conn); message["type"] != protocol.TypeHello {
		t.Fatalf("first response = %#v, want hello", message)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send ping after last_seen_at failure: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("response after last_seen_at failure = %#v, want pong", message)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send second ping after last_seen_at failure: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("response after second last_seen_at failure = %#v, want pong", message)
	}
	if !f.list.Online(testMachineID) {
		t.Fatal("connection is not Online after last_seen_at write failure")
	}
	if got := f.logs.String(); !strings.Contains(got, `kind="write last seen"`) {
		t.Fatalf("log = %q, want write last seen kind", got)
	}
	if got := strings.Count(f.logs.String(), `kind="write last seen"`); got < 3 {
		t.Fatalf("last_seen_at write failure log count = %d, want connect failure and retries on both messages", got)
	}
}

func TestOriginRefused(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	headers := make(http.Header)
	headers.Set("Origin", "https://browser.example")
	f.pool.Close()
	_, response, err := f.dial(t, testCredential, headers)
	if err == nil {
		t.Fatal("request with Origin opened a WebSocket")
	}
	assertJSONError(t, response, http.StatusForbidden, "forbidden_origin")
	// Closing the pool proves the Origin check happened before credential lookup.
}

func TestHelloCarriesTiming(t *testing.T) {
	f := newRouteFixture(t, 4*time.Second, 13*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	message := readJSON(t, conn)
	if message["type"] != protocol.TypeHello || message["check_interval_seconds"] != float64(4) || message["timeout_seconds"] != float64(13) {
		t.Fatalf("hello = %#v, want configured timing", message)
	}
}

func TestPingGetsPong(t *testing.T) {
	f := newRouteFixture(t, 1*time.Second, 3*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	_ = readJSON(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send ping: %v", err)
	}
	message := readJSON(t, conn)
	if message["type"] != protocol.TypePong {
		t.Fatalf("response type = %#v, want pong", message["type"])
	}
}

func TestServerTimeout(t *testing.T) {
	f := newRouteFixture(t, time.Second, 3*time.Second)
	silent, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open silent WebSocket: %v", err)
	}
	_ = readJSON(t, silent)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = silent.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseTimeout {
		t.Fatalf("silent client close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseTimeout)
	}
	deadline := time.Now().Add(time.Second)
	for f.list.Online(testMachineID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.list.Online(testMachineID) {
		t.Fatal("timed out machine remains Online")
	}

	active, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open active WebSocket: %v", err)
	}
	defer active.Close(websocket.StatusNormalClosure, "test done")
	_ = readJSON(t, active)
	for range 8 {
		if err := active.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
			t.Fatalf("send keepalive ping: %v", err)
		}
		message := readJSON(t, active)
		if message["type"] != protocol.TypePong {
			t.Fatalf("keepalive response = %#v, want pong", message)
		}
		time.Sleep(750 * time.Millisecond)
	}
	if !f.list.Online(testMachineID) {
		t.Fatal("machine sending pings is not Online")
	}
}

func TestClientCloseNoticedAtOnce(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	if !f.list.Online(testMachineID) {
		t.Fatal("machine is not Online after connecting")
	}
	if err := conn.Close(websocket.StatusNormalClosure, "worker stopping"); err != nil {
		t.Fatalf("close client WebSocket: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for f.list.Online(testMachineID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.list.Online(testMachineID) {
		t.Fatal("server did not notice client close promptly")
	}
}

func TestAddAfterCloseAll(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	tx, err := f.pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin row-lock transaction: %v", err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `SELECT machine_id FROM machines WHERE machine_id = $1 FOR UPDATE`, testMachineID); err != nil {
		t.Fatalf("lock machine row: %v", err)
	}
	f.list.CloseAll()
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("complete handshake after CloseAll: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseServerStopping {
		t.Fatalf("post-stop connection close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseServerStopping)
	}
	if f.list.Online(testMachineID) {
		t.Fatal("post-stop connection was stored as Online")
	}
	closeAllDone := make(chan struct{})
	go func() {
		f.list.CloseAll()
		close(closeAllDone)
	}()
	select {
	case <-closeAllDone:
		t.Fatal("CloseAll returned while the post-stop handler was blocked on its machine update")
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("release machine row lock: %v", err)
	}
	select {
	case <-closeAllDone:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseAll did not return after the post-stop handler finished")
	}
	var lastSeen *time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&lastSeen); err != nil {
		t.Fatalf("read last_seen_at after second CloseAll: %v", err)
	}
	if lastSeen == nil {
		t.Fatal("post-stop handler did not write final last_seen_at")
	}
}

func TestNotAnUpgradeRequest(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "no upgrade headers", mutate: func(*http.Request) {}},
		{name: "missing key", mutate: func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Version", "13")
		}},
		{name: "wrong version", mutate: func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Version", "12")
			r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		}},
		{name: "wrong upgrade protocol", mutate: func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket, other")
			r.Header.Set("Sec-WebSocket-Version", "13")
			r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		}},
		{name: "malformed key", mutate: func(r *http.Request) {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Version", "13")
			r.Header.Set("Sec-WebSocket-Key", "not-a-key")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/machines/current/connection", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+testCredential)
			test.mutate(req)
			response, err := f.server.Client().Do(req)
			if err != nil {
				t.Fatalf("send request: %v", err)
			}
			assertJSONError(t, response, http.StatusBadRequest, "invalid_input")
		})
	}
}

func TestNewestWins(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	first, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open first connection: %v", err)
	}
	_ = readJSON(t, first)
	second, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	defer second.Close(websocket.StatusNormalClosure, "test done")
	_ = readJSON(t, second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = first.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseReplaced {
		t.Fatalf("old connection close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseReplaced)
	}
	if !f.list.Online(testMachineID) {
		t.Fatal("machine went Offline after replacement")
	}
	if !strings.Contains(f.logs.String(), "machine connection replaced") || !strings.Contains(f.logs.String(), "machine_id="+testMachineID) {
		t.Fatalf("log = %q, want replacement warning with machine id", f.logs.String())
	}
}

func TestReplacedCloseKeepsNewEntry(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	first, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open first connection: %v", err)
	}
	_ = readJSON(t, first)
	second, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	defer second.Close(websocket.StatusNormalClosure, "test done")
	_ = readJSON(t, second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, _ = first.Read(ctx)
	waitForHandlerCount(t, f.list, 1)
	if !f.list.Online(testMachineID) {
		t.Fatal("old connection removal removed the replacement")
	}
}

func waitForHandlerCount(t *testing.T, list *List, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		list.mu.Lock()
		got := len(list.handlers)
		list.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	list.mu.Lock()
	got := len(list.handlers)
	list.mu.Unlock()
	t.Fatalf("tracked handler count = %d, want %d", got, want)
}

func TestListSafeUnderLoad(t *testing.T) {
	list := NewList()
	var wait sync.WaitGroup
	for worker := range 32 {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := range 250 {
				id := fmt.Sprintf("machine-%d", (worker+i)%12)
				conn := &testSocket{}
				list.add(id, conn)
				if i%2 == 0 {
					list.remove(id, conn)
				}
				_ = list.Online(id)
				_ = list.OnlineIDs()
			}
		}()
	}
	wait.Wait()
	list.CloseAll()
}

type testSocket struct {
	mu    sync.Mutex
	codes []websocket.StatusCode
}

func (c *testSocket) Close(code websocket.StatusCode, _ string) error {
	c.mu.Lock()
	c.codes = append(c.codes, code)
	c.mu.Unlock()
	return nil
}

func (c *testSocket) CloseNow() error { return nil }

type forceCloseSocket struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newForceCloseSocket() *forceCloseSocket {
	return &forceCloseSocket{closed: make(chan struct{})}
}

func (c *forceCloseSocket) Close(websocket.StatusCode, string) error {
	<-c.closed
	return nil
}

func (c *forceCloseSocket) CloseNow() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestCloseAllSharesDeadlineAndForcesClose(t *testing.T) {
	list := NewList()
	fake := newForceCloseSocket()
	setConnection, finish := list.trackHandler()
	setConnection(fake)
	if !list.add("machine", fake) {
		t.Fatal("add() rejected the connection before shutdown")
	}
	handlerDone := make(chan struct{})
	go func() {
		<-fake.closed
		finish()
		close(handlerDone)
	}()
	t.Cleanup(func() {
		_ = fake.CloseNow()
		finish()
	})

	started := time.Now()
	list.CloseAll()
	list.CloseAll()
	list.CloseAll()
	elapsed := time.Since(started)
	if elapsed >= 5500*time.Millisecond {
		t.Errorf("three CloseAll() calls took %s, want one shared deadline under 5.5s", elapsed)
	}
	select {
	case <-fake.closed:
	default:
		t.Error("CloseNow() was not called for a connection that did not answer Close()")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Error("tracked handler did not finish after the fake connection closed")
	}
}

func TestOnlineListStartsEmpty(t *testing.T) {
	list := NewList()
	if list.Online("machine") || len(list.OnlineIDs()) != 0 {
		t.Fatal("new connection list is not empty")
	}
}

func TestRemoveOnlyRemovesCurrentConnection(t *testing.T) {
	list := NewList()
	a := &testSocket{}
	b := &testSocket{}
	list.add("machine", a)
	list.add("machine", b)
	list.remove("machine", a)
	if !list.Online("machine") {
		t.Fatal("removing a replaced connection removed the current entry")
	}
	list.remove("machine", b)
	if list.Online("machine") {
		t.Fatal("removing the current connection left the machine Online")
	}
	list.CloseAll()
}

func TestLastSeen(t *testing.T) {
	f := newRouteFixture(t, time.Second, 5*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	var first time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&first); err != nil {
		t.Fatalf("read connect last_seen_at: %v", err)
	}
	threshold := time.Minute - time.Second
	f.clock.Advance(threshold - time.Millisecond)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send just-before-threshold ping: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("just-before-threshold response = %#v, want pong", message["type"])
	}
	var beforeMinute time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&beforeMinute); err != nil {
		t.Fatalf("read last_seen_at before minute: %v", err)
	}
	if !beforeMinute.Equal(first) {
		t.Fatalf("last_seen_at changed before threshold: %s to %s", first, beforeMinute)
	}
	f.clock.Advance(time.Millisecond)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send at-threshold ping: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("at-threshold response = %#v, want pong", message["type"])
	}
	deadline := time.Now().Add(time.Second)
	var afterMinute time.Time
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&afterMinute); err != nil {
			t.Fatalf("read last_seen_at after threshold: %v", err)
		}
		if afterMinute.After(beforeMinute) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !afterMinute.After(beforeMinute) {
		t.Fatalf("last_seen_at did not update at threshold: %s", afterMinute)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := conn.Close(websocket.StatusNormalClosure, "test done"); err != nil {
		t.Fatalf("close WebSocket: %v", err)
	}
	f.list.CloseAll()
	var final time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&final); err != nil {
		t.Fatalf("read final last_seen_at: %v", err)
	}
	if age := time.Since(final); age < time.Second || age > 2*time.Second {
		t.Fatalf("final last_seen_at age = %s, want about one second since the last message", age)
	}
}

func TestLastSeenThresholdUsesTenSecondInterval(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 90*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	var first time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&first); err != nil {
		t.Fatalf("read connect last_seen_at: %v", err)
	}
	threshold := 50 * time.Second
	f.clock.Advance(threshold - time.Millisecond)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send just-before-threshold ping: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("just-before-threshold response = %#v, want pong", message)
	}
	var before time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&before); err != nil {
		t.Fatalf("read last_seen_at before threshold: %v", err)
	}
	if !before.Equal(first) {
		t.Fatalf("last_seen_at changed before 50 second threshold: %s to %s", first, before)
	}
	f.clock.Advance(time.Millisecond)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send at-threshold ping: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("at-threshold response = %#v, want pong", message)
	}
	deadline := time.Now().Add(time.Second)
	var after time.Time
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&after); err != nil {
			t.Fatalf("read last_seen_at after threshold: %v", err)
		}
		if after.After(before) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("last_seen_at did not update at the 50 second threshold: %s", after)
}

func TestOversizedMessageCloses(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, bytes.Repeat([]byte("x"), protocol.MaxMessageSize+1)); err != nil {
		t.Fatalf("send oversized message: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseTooLarge {
		t.Fatalf("oversized message close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseTooLarge)
	}
}

func TestUnknownTypeIgnored(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	_ = readJSON(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"future"}`)); err != nil {
		t.Fatalf("send unknown message: %v", err)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("send ping after unknown message: %v", err)
	}
	if message := readJSON(t, conn); message["type"] != protocol.TypePong {
		t.Fatalf("response after unknown type = %#v, want pong", message)
	}
}

func TestMalformedMessageCloses(t *testing.T) {
	for _, message := range []string{`{"type":7}`, `{}`, `[]`, `{`} {
		t.Run(fmt.Sprintf("message_%q", message), func(t *testing.T) {
			f := newRouteFixture(t, 10*time.Second, 30*time.Second)
			conn, _, err := f.dial(t, testCredential, nil)
			if err != nil {
				t.Fatalf("open WebSocket: %v", err)
			}
			_ = readJSON(t, conn)
			if err := conn.Write(context.Background(), websocket.MessageText, []byte(message)); err != nil {
				t.Fatalf("send malformed message: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, err = conn.Read(ctx)
			if websocket.CloseStatus(err) != protocol.CloseInvalidMessage {
				t.Fatalf("malformed message close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseInvalidMessage)
			}
		})
	}
}

func TestCredentialNeverLogged(t *testing.T) {
	f := newRouteFixture(t, 10*time.Second, 30*time.Second)
	conn, _, err := f.dial(t, testCredential, nil)
	if err != nil {
		t.Fatalf("open WebSocket: %v", err)
	}
	_ = readJSON(t, conn)
	if err := conn.Close(websocket.StatusNormalClosure, "test done"); err != nil {
		t.Fatalf("close WebSocket: %v", err)
	}
	if strings.Contains(f.logs.String(), testCredential) {
		t.Fatal("connection logs contain the credential")
	}
}
