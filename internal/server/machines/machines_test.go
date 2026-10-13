package machines_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type machine struct {
	MachineID          string `json:"machine_id"`
	DisplayName        string `json:"display_name"`
	OSName             string `json:"os_name"`
	OSVersion          string `json:"os_version"`
	Online             bool   `json:"online"`
	LastSeenSecondsAgo *int64 `json:"last_seen_seconds_ago"`
}

type machineList struct {
	Machines []machine `json:"machines"`
}

type fixture struct {
	pool   *pgxpool.Pool
	list   *connection.List
	logs   *bytes.Buffer
	server *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.New(t)
	list := connection.NewList()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	handler := server.New(server.Config{}, pool, logger, list)
	ts := httptest.NewServer(handler)
	f := &fixture{pool: pool, list: list, logs: logs, server: ts}
	t.Cleanup(func() {
		list.CloseAll()
		ts.Close()
	})
	return f
}

func (f *fixture) addMachine(t *testing.T, id, name, osName, osVersion, status, credential string) string {
	t.Helper()
	ctx := context.Background()
	var pairingID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('paired', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("polling-"+credential), []byte("approval-"+credential)).Scan(&pairingID); err != nil {
		t.Fatalf("insert pairing request: %v", err)
	}
	var machineID string
	hash := secret.Hash(secret.FromString(credential))
	if id == "" {
		err := f.pool.QueryRow(ctx, `INSERT INTO machines
			(pairing_request_id, display_name, os_name, os_version, credential_hash, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, now() + interval '10 minutes') RETURNING machine_id::text`,
			pairingID, name, osName, osVersion, hash, status).Scan(&machineID)
		if err != nil {
			t.Fatalf("insert machine: %v", err)
		}
		return machineID
	}
	err := f.pool.QueryRow(ctx, `INSERT INTO machines
			(machine_id, pairing_request_id, display_name, os_name, os_version, credential_hash, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '10 minutes') RETURNING machine_id::text`,
		id, pairingID, name, osName, osVersion, hash, status).Scan(&machineID)
	if err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	return machineID
}

func (f *fixture) listMachines(t *testing.T) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/machines", nil)
	if err != nil {
		t.Fatalf("build machines request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET /api/v1/machines: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read machines response: %v", err)
	}
	return response.StatusCode, body
}

func decodeMachineList(t *testing.T, body []byte) machineList {
	t.Helper()
	var answer machineList
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode machines list: %v; body = %q", err, body)
	}
	return answer
}

func TestListOnlyActiveSorted(t *testing.T) {
	f := newFixture(t)
	f.addMachine(t, "", "zeta", "Linux", "1", "active", "list-sort-zeta")
	f.addMachine(t, "22222222-2222-4222-8222-222222222222", "alpha", "Linux", "1", "active", "list-sort-alpha-two")
	f.addMachine(t, "11111111-1111-4111-8111-111111111111", "alpha", "Linux", "1", "active", "list-sort-alpha-one")
	f.addMachine(t, "", "aardvark", "Linux", "1", "pending", "list-sort-pending")

	status, body := f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	answer := decodeMachineList(t, body)
	want := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
	}
	if len(answer.Machines) != 3 {
		t.Fatalf("machine count = %d, want 3; body = %q", len(answer.Machines), body)
	}
	if got := []string{answer.Machines[0].MachineID, answer.Machines[1].MachineID}; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("equal-name machine IDs = %v, want %v", got, want)
	}
	if got := answer.Machines[2].DisplayName; got != "zeta" {
		t.Errorf("third display name = %q, want zeta", got)
	}
}

func TestListOnlineFromMemory(t *testing.T) {
	f := newFixture(t)
	credential := "online-list-credential"
	onlineID := f.addMachine(t, "", "online", "Linux", "1", "active", credential)
	offlineID := f.addMachine(t, "", "offline", "Linux", "1", "active", "offline-list-credential")

	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/machines/current/connection", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + credential}},
	})
	if err != nil {
		t.Fatalf("open machine WebSocket: %v", err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(context.Background()); err != nil {
		t.Fatalf("read machine hello: %v", err)
	}

	status, body := f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	answer := decodeMachineList(t, body)
	got := make(map[string]bool, len(answer.Machines))
	for _, listed := range answer.Machines {
		got[listed.MachineID] = listed.Online
	}
	if got[onlineID] != true {
		t.Errorf("online machine %s online = %t, want true", onlineID, got[onlineID])
	}
	if got[offlineID] != false {
		t.Errorf("offline machine %s online = %t, want false", offlineID, got[offlineID])
	}
}

func TestListLastSeen(t *testing.T) {
	f := newFixture(t)
	seenID := f.addMachine(t, "", "seen", "Linux", "1", "active", "last-seen-value")
	neverID := f.addMachine(t, "", "never", "Linux", "1", "active", "last-seen-null")
	futureID := f.addMachine(t, "", "future", "Linux", "1", "active", "last-seen-future")
	if _, err := f.pool.Exec(context.Background(), `UPDATE machines SET last_seen_at = now() - interval '75.9 seconds' WHERE machine_id = $1`, seenID); err != nil {
		t.Fatalf("set seen machine time: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE machines SET last_seen_at = now() + interval '1 hour' WHERE machine_id = $1`, futureID); err != nil {
		t.Fatalf("set future machine time: %v", err)
	}

	status, body := f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	answer := decodeMachineList(t, body)
	got := make(map[string]*int64, len(answer.Machines))
	for _, listed := range answer.Machines {
		got[listed.MachineID] = listed.LastSeenSecondsAgo
	}
	if got[seenID] == nil || *got[seenID] != 75 {
		t.Errorf("seen last_seen_seconds_ago = %v, want 75", got[seenID])
	}
	if _, exists := got[neverID]; !exists || got[neverID] != nil {
		t.Errorf("never-connected last_seen_seconds_ago = %v, want null", got[neverID])
	}
	if got[futureID] == nil || *got[futureID] != 0 {
		t.Errorf("future last_seen_seconds_ago = %v, want 0", got[futureID])
	}
}

func TestListHasNoCredential(t *testing.T) {
	f := newFixture(t)
	credential := "credential-must-not-appear-in-machines-list"
	f.addMachine(t, "", "safe machine", "Linux", "1", "active", credential)

	status, body := f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	hash := secret.Hash(secret.FromString(credential))
	if strings.Contains(string(body), credential) {
		t.Fatal("machines response contains a credential")
	}
	if strings.Contains(string(body), base64.StdEncoding.EncodeToString(hash)) || strings.Contains(string(body), "credential_hash") {
		t.Fatal("machines response contains a credential hash")
	}
}

func TestListDatabaseFailure(t *testing.T) {
	f := newFixture(t)
	f.pool.Close()

	status, body := f.listMachines(t)
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusInternalServerError, body)
	}
	var answer map[string]string
	if err := json.Unmarshal(body, &answer); err != nil || answer["error"] != "internal_error" {
		t.Fatalf("error response = %q, want internal_error", body)
	}
	if !strings.Contains(f.logs.String(), `kind="list machines"`) {
		t.Fatalf("log = %q, want kind list machines", f.logs.String())
	}
}
