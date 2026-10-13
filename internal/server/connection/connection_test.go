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
	pool   *pgxpool.Pool
	list   *List
	logger *slog.Logger
	logs   *bytes.Buffer
	server *httptest.Server
	clock  *testClock
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
	mux := http.NewServeMux()
	Register(mux, Deps{Pool: pool, Logger: logger, List: list, CheckInterval: interval, ConnectionTimeout: timeout, Now: clock.Now})
	server := httptest.NewServer(mux)
	fixture := &routeFixture{pool: pool, list: list, logger: logger, logs: logs, server: server, clock: clock}
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
	ctx := context.Background()
	var pairingID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("polling-"+status+credential), []byte("approval-"+status+credential)).Scan(&pairingID); err != nil {
		t.Fatalf("insert pairing request: %v", err)
	}
	var machineID string
	query := `INSERT INTO machines
		(pairing_request_id, display_name, credential_hash, status, expires_at)
		VALUES ($1, 'test machine', $2, $3, now() + interval '10 minutes') RETURNING machine_id::text`
	args := []any{pairingID, secret.Hash(secret.FromString(credential)), status}
	if credential == testCredential {
		query = `INSERT INTO machines
			(machine_id, pairing_request_id, display_name, credential_hash, status, expires_at)
			VALUES ($1, $2, 'test machine', $3, $4, now() + interval '10 minutes') RETURNING machine_id::text`
		args = []any{testMachineID, pairingID, secret.Hash(secret.FromString(credential)), status}
	}
	if err := f.pool.QueryRow(ctx, query, args...).Scan(&machineID); err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	return machineID
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
	if strings.Contains(f.logs.String(), "kind=look up machine") {
		t.Fatalf("Origin was checked after credential lookup: %q", f.logs.String())
	}
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
	for range 4 {
		if err := active.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
			t.Fatalf("send keepalive ping: %v", err)
		}
		message := readJSON(t, active)
		if message["type"] != protocol.TypePong {
			t.Fatalf("keepalive response = %#v, want pong", message)
		}
		time.Sleep(600 * time.Millisecond)
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
	f.list.CloseAll()
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
	if !f.list.Online(testMachineID) {
		t.Fatal("old connection removal removed the replacement")
	}
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

func TestOnlineListStartsEmpty(t *testing.T) {
	list := NewList()
	if list.Online("machine") || len(list.OnlineIDs()) != 0 {
		t.Fatal("new connection list is not empty")
	}
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
	f.clock.Advance(59 * time.Second)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"other"}`)); err != nil {
		t.Fatalf("send first message: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	var beforeMinute time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&beforeMinute); err != nil {
		t.Fatalf("read last_seen_at before minute: %v", err)
	}
	if !beforeMinute.Equal(first) {
		t.Fatalf("last_seen_at changed before one minute: %s to %s", first, beforeMinute)
	}
	f.clock.Advance(time.Second)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"other"}`)); err != nil {
		t.Fatalf("send minute message: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	var afterMinute time.Time
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, testMachineID).Scan(&afterMinute); err != nil {
			t.Fatalf("read last_seen_at after minute: %v", err)
		}
		if afterMinute.After(beforeMinute) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !afterMinute.After(beforeMinute) {
		t.Fatalf("last_seen_at did not update after one minute: %s", afterMinute)
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
