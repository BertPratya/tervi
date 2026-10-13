package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/coder/websocket"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()

	server.New(server.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), connection.NewList()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHostCheck(t *testing.T) {
	handler := server.New(server.Config{PublicURL: "http://tervi.test:8080"}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), connection.NewList())

	for _, host := range []string{"localhost:8080", "LOCALHOST:8080", "127.0.0.1:9000", "[::1]:8080", "tervi.test"} {
		t.Run("allowed_"+host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
			}
			if body := rec.Body.String(); body != "ok" {
				t.Errorf("body = %q, want %q", body, "ok")
			}
		})
	}

	for _, host := range []string{"evil.example:8080", "example.com", "127.0.0.1.evil.example", ""} {
		t.Run("rejected_"+host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want %q", got, "application/json")
			}
			if body := rec.Body.String(); body != `{"error": "unknown_host"}` {
				t.Errorf("body = %q, want %q", body, `{"error": "unknown_host"}`)
			}
		})
	}
}

func TestRunRefusesUnmigratedDatabase(t *testing.T) {
	pool, databaseURL := dbtest.NewEmpty(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = server.Run(ctx, server.Config{ServerAddr: address, PublicURL: "http://localhost", DatabaseURL: databaseURL}, nil)
	if !errors.Is(err, db.ErrNotMigrated) {
		t.Fatalf("Run() error = %v, want ErrNotMigrated", err)
	}
	conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("server accepted connections at %s", address)
	}
	var tableCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema()`).Scan(&tableCount); err != nil {
		t.Fatalf("count tables in empty database: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("Run() created %d tables in an unmigrated database", tableCount)
	}
}

func TestCloseAllOnStop(t *testing.T) {
	pool, databaseURL := dbtest.NewEmpty(t)
	if _, err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	credentials := []string{"server-stop-credential-one", "server-stop-credential-two"}
	machineIDs := make([]string, 0, len(credentials))
	for i, credential := range credentials {
		var pairingID string
		if err := pool.QueryRow(context.Background(), `INSERT INTO pairing_requests
			(status, polling_key_hash, approval_key_hash, expires_at)
			VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
			[]byte(strings.Repeat("p", i+1)), []byte(strings.Repeat("a", i+1))).Scan(&pairingID); err != nil {
			t.Fatalf("insert pairing request: %v", err)
		}
		var machineID string
		if err := pool.QueryRow(context.Background(), `INSERT INTO machines
			(pairing_request_id, display_name, credential_hash, status, expires_at)
			VALUES ($1, $2, $3, 'active', now() + interval '10 minutes') RETURNING machine_id::text`, pairingID, "server-stop-machine", secret.Hash(secret.FromString(credential))).Scan(&machineID); err != nil {
			t.Fatalf("insert machine: %v", err)
		}
		machineIDs = append(machineIDs, machineID)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve server address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release server address: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Config{
			ServerAddr: address, PublicURL: "http://127.0.0.1", DatabaseURL: databaseURL,
			CheckInterval: time.Second, ConnectionTimeout: 4 * time.Second,
		}, nil)
	}()
	t.Cleanup(cancel)

	connections := make([]*websocket.Conn, 0, len(credentials))
	helloTimes := make([]time.Time, 0, len(credentials))
	for index, credential := range credentials {
		var conn *websocket.Conn
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			headers := make(http.Header)
			headers.Set("Authorization", "Bearer "+credential)
			conn, _, err = websocket.Dial(context.Background(), "ws://"+address+"/api/v1/machines/current/connection", &websocket.DialOptions{HTTPHeader: headers})
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			cancel()
			t.Fatalf("open WebSocket %d: %v", index+1, err)
		}
		connections = append(connections, conn)
		readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
		_, message, readErr := conn.Read(readCtx)
		readCancel()
		if readErr != nil || !strings.Contains(string(message), `"type":"hello"`) {
			t.Fatalf("read hello for WebSocket %d: message=%q error=%v", index+1, message, readErr)
		}
		helloTimes = append(helloTimes, time.Now())
		if _, err := pool.Exec(context.Background(), `UPDATE machines SET last_seen_at = NULL WHERE machine_id = $1`, machineIDs[index]); err != nil {
			t.Fatalf("clear connect-time last_seen_at for WebSocket %d: %v", index+1, err)
		}
	}
	closeErrors := make(chan error, len(connections))
	for _, conn := range connections {
		go func(conn *websocket.Conn) {
			_, _, readErr := conn.Read(context.Background())
			closeErrors <- readErr
		}(conn)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after cancellation: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("Run() did not finish after cancellation")
	}
	for index, conn := range connections {
		readErr := <-closeErrors
		_ = conn.Close(websocket.StatusNormalClosure, "test cleanup")
		if websocket.CloseStatus(readErr) != protocol.CloseServerStopping {
			t.Errorf("WebSocket %d close status = %d (%v), want %d", index+1, websocket.CloseStatus(readErr), readErr, protocol.CloseServerStopping)
		}
	}
	for index, machineID := range machineIDs {
		var lastSeen *time.Time
		if err := pool.QueryRow(context.Background(), `SELECT last_seen_at FROM machines WHERE machine_id = $1`, machineID).Scan(&lastSeen); err != nil {
			t.Fatalf("read final last_seen_at for WebSocket %d: %v", index+1, err)
		}
		if lastSeen == nil {
			t.Errorf("WebSocket %d final last_seen_at is null", index+1)
			continue
		}
		if difference := lastSeen.Sub(helloTimes[index]); difference < -time.Second || difference > time.Second {
			t.Errorf("WebSocket %d final last_seen_at differs from hello time by %s, want within 1s", index+1, difference)
		}
	}
}

func TestRunBoundsStopWhenWorkerDoesNotAnswerClose(t *testing.T) {
	pool, databaseURL := dbtest.NewEmpty(t)
	if _, err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	credential := "server-stop-silent-worker-credential"
	var pairingID string
	if err := pool.QueryRow(context.Background(), `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("polling-silent-worker"), []byte("approval-silent-worker")).Scan(&pairingID); err != nil {
		t.Fatalf("insert pairing request: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO machines
		(pairing_request_id, display_name, credential_hash, status, expires_at)
		VALUES ($1, 'silent worker', $2, 'active', now() + interval '10 minutes')`,
		pairingID, secret.Hash(secret.FromString(credential))); err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve server address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release server address: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Config{
			ServerAddr: address, PublicURL: "http://127.0.0.1", DatabaseURL: databaseURL,
			CheckInterval: time.Second, ConnectionTimeout: 30 * time.Second,
		}, nil)
	}()
	var conn *websocket.Conn
	t.Cleanup(func() {
		cancel()
		if conn != nil {
			_ = conn.CloseNow()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		headers := make(http.Header)
		headers.Set("Authorization", "Bearer "+credential)
		conn, _, err = websocket.Dial(context.Background(), "ws://"+address+"/api/v1/machines/current/connection", &websocket.DialOptions{HTTPHeader: headers})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("open silent worker WebSocket: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	_, message, err := conn.Read(readCtx)
	readCancel()
	if err != nil || !strings.Contains(string(message), `"type":"hello"`) {
		t.Fatalf("read hello from silent worker: message=%q error=%v", message, err)
	}

	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after cancellation: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Run() did not return within about five seconds when the worker did not answer close")
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("Run() stop took %s, want about five seconds", elapsed)
	}
}
