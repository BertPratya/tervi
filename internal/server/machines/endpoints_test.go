package machines_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/coder/websocket"
)

func (f *fixture) postEndpoint(t *testing.T, path, proof string, withOrigin bool) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build POST %s request: %v", path, err)
	}
	if proof != "" {
		request.Header.Set("Authorization", "Bearer "+proof)
	}
	if withOrigin {
		request.Header.Set("Origin", "https://browser.example")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read POST %s response: %v", path, err)
	}
	return response.StatusCode, body
}

func TestUnpairRevokes(t *testing.T) {
	f := newFixture(t)
	const proof = "unpair-revokes-proof"
	machineID := f.addMachine(t, "", "unpair target", "Linux", "1", "active", proof)

	status, body := f.postEndpoint(t, "/api/v1/machines/current/unpair", proof, false)
	if status != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("unpair response = (%d, %q), want 204 with no body", status, body)
	}
	assertCredentialRevoked(t, f, machineID)

	request, err := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/machines/current/connection", nil)
	if err != nil {
		t.Fatalf("build connection request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+proof)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET connection after unpair: %v", err)
	}
	defer response.Body.Close()
	connectionBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read refused connection response: %v", err)
	}
	assertEndpointError(t, response.StatusCode, connectionBody, http.StatusUnauthorized, "credential_revoked")
}

func TestUnpairRepeat(t *testing.T) {
	f := newFixture(t)
	const proof = "unpair-repeat-proof"
	machineID := f.addMachine(t, "", "unpair repeat", "Linux", "1", "active", proof)
	var firstEndedAt time.Time

	for attempt := 1; attempt <= 2; attempt++ {
		status, body := f.postEndpoint(t, "/api/v1/machines/current/unpair", proof, false)
		if status != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("unpair attempt %d response = (%d, %q), want 204 with no body", attempt, status, body)
		}
		var credentialStatus string
		var endedAt *time.Time
		if err := f.pool.QueryRow(context.Background(), `SELECT status, ended_at FROM credentials WHERE machine_id = $1`, machineID).
			Scan(&credentialStatus, &endedAt); err != nil {
			t.Fatalf("read credential after attempt %d: %v", attempt, err)
		}
		if credentialStatus != "revoked" || endedAt == nil {
			t.Fatalf("attempt %d credential = (%q, %v), want revoked with ended_at", attempt, credentialStatus, endedAt)
		}
		if attempt == 1 {
			firstEndedAt = *endedAt
		} else if !endedAt.Equal(firstEndedAt) {
			t.Fatalf("repeat changed ended_at from %s to %s", firstEndedAt, endedAt)
		}
	}
}

func TestUnpairUnknown(t *testing.T) {
	f := newFixture(t)
	proofs := []string{"", "unpair-unknown-proof"}
	for _, credentialStatus := range []string{"pending", "failed", "expired"} {
		proof := "unpair-" + credentialStatus + "-proof"
		f.addMachine(t, "", credentialStatus, "Linux", "1", credentialStatus, proof)
		if credentialStatus == "expired" {
			if _, err := f.pool.Exec(context.Background(), `UPDATE credentials SET expires_at = now() - interval '1 second'
				WHERE credential_hash = $1`, secret.Hash(secret.FromString(proof))); err != nil {
				t.Fatalf("expire fixture credential: %v", err)
			}
		}
		proofs = append(proofs, proof)
	}

	for _, proof := range proofs {
		status, body := f.postEndpoint(t, "/api/v1/machines/current/unpair", proof, false)
		assertEndpointError(t, status, body, http.StatusUnauthorized, "unknown_credential")
	}
}

func TestUnpairClosesConnection(t *testing.T) {
	f := newFixture(t)
	const proof = "unpair-connected-proof"
	machineID := f.addMachine(t, "", "connected unpair", "Linux", "1", "active", proof)
	conn := dialEndpointMachine(t, f.server.URL, proof)
	defer conn.CloseNow()
	readEndpointHello(t, conn)
	if !f.list.Online(machineID) {
		t.Fatal("machine is not Online after opening its connection")
	}

	status, body := f.postEndpoint(t, "/api/v1/machines/current/unpair", proof, false)
	if status != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("unpair response = (%d, %q), want 204 with no body", status, body)
	}
	if f.list.Online(machineID) {
		t.Fatal("machine remains Online after unpair")
	}
	assertEndpointConnectionClosed(t, conn)
	status, body = f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	if listed := decodeMachineList(t, body); len(listed.Machines) != 0 {
		t.Fatalf("unpaired machine remains listed: %+v", listed.Machines)
	}
}

func TestUnpairDatabaseFailure(t *testing.T) {
	f := newFixture(t)
	f.pool.Close()
	status, body := f.postEndpoint(t, "/api/v1/machines/current/unpair", "unpair-db-failure-proof", false)
	assertEndpointError(t, status, body, http.StatusInternalServerError, "internal_error")
	if !strings.Contains(f.logs.String(), `kind="unpair machine"`) {
		t.Fatalf("log = %q, want kind unpair machine", f.logs.String())
	}
}

func TestRevokeMachine(t *testing.T) {
	f := newFixture(t)
	const proof = "revoke-machine-proof"
	machineID := f.addMachine(t, "", "revoke target", "Linux", "1", "active", proof)

	status, body := f.postEndpoint(t, "/api/v1/machines/"+machineID+"/revoke", "", false)
	if status != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("revoke response = (%d, %q), want 204 with no body", status, body)
	}
	assertCredentialRevoked(t, f, machineID)
	status, body = f.listMachines(t)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/machines status = %d, want %d; body = %q", status, http.StatusOK, body)
	}
	for _, listed := range decodeMachineList(t, body).Machines {
		if listed.MachineID == machineID {
			t.Fatalf("revoked machine %s remains listed", machineID)
		}
	}
	var machineCount int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM machines WHERE machine_id = $1`, machineID).Scan(&machineCount); err != nil {
		t.Fatalf("count retained machine row: %v", err)
	}
	if machineCount != 1 {
		t.Fatalf("machine row count = %d, want 1", machineCount)
	}
}

func TestRevokeClosesConnection(t *testing.T) {
	f := newFixture(t)
	const proof = "revoke-connected-proof"
	machineID := f.addMachine(t, "", "connected revoke", "Linux", "1", "active", proof)
	conn := dialEndpointMachine(t, f.server.URL, proof)
	defer conn.CloseNow()
	readEndpointHello(t, conn)
	if !f.list.Online(machineID) {
		t.Fatal("machine is not Online after opening its connection")
	}

	status, body := f.postEndpoint(t, "/api/v1/machines/"+machineID+"/revoke", "", false)
	if status != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("revoke response = (%d, %q), want 204 with no body", status, body)
	}
	if f.list.Online(machineID) {
		t.Fatal("machine remains Online after revoke")
	}
	assertEndpointConnectionClosed(t, conn)

	request, err := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/machines/current/connection", nil)
	if err != nil {
		t.Fatalf("build reconnect request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+proof)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET connection after revoke: %v", err)
	}
	defer response.Body.Close()
	reconnectBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read refused reconnect response: %v", err)
	}
	assertEndpointError(t, response.StatusCode, reconnectBody, http.StatusUnauthorized, "credential_revoked")
}

func TestRevokeRepeatAndUnknown(t *testing.T) {
	f := newFixture(t)
	const proof = "revoke-repeat-proof"
	machineID := f.addMachine(t, "", "revoke repeat", "Linux", "1", "active", proof)
	for attempt := 1; attempt <= 2; attempt++ {
		status, body := f.postEndpoint(t, "/api/v1/machines/"+machineID+"/revoke", "", false)
		if status != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("revoke attempt %d response = (%d, %q), want 204 with no body", attempt, status, body)
		}
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000000001", "not-a-uuid"} {
		status, body := f.postEndpoint(t, "/api/v1/machines/"+id+"/revoke", "", false)
		assertEndpointError(t, status, body, http.StatusNotFound, "unknown_machine")
	}
}

func TestRevokeNeedsNoProof(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		proof      string
		withOrigin bool
	}{
		{proof: "revoke-no-proof-no-origin"},
		{proof: "revoke-no-proof-origin", withOrigin: true},
	} {
		machineID := f.addMachine(t, "", "no proof revoke", "Linux", "1", "active", test.proof)
		status, body := f.postEndpoint(t, "/api/v1/machines/"+machineID+"/revoke", "", test.withOrigin)
		if status != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("revoke without proof (Origin present: %t) response = (%d, %q), want 204 with no body", test.withOrigin, status, body)
		}
	}
}

func TestRevokeDatabaseFailure(t *testing.T) {
	f := newFixture(t)
	f.pool.Close()
	status, body := f.postEndpoint(t, "/api/v1/machines/00000000-0000-4000-8000-000000000001/revoke", "", false)
	assertEndpointError(t, status, body, http.StatusInternalServerError, "internal_error")
	if !strings.Contains(f.logs.String(), `kind="revoke machine"`) {
		t.Fatalf("log = %q, want kind revoke machine", f.logs.String())
	}
}

func assertCredentialRevoked(t *testing.T, f *fixture, machineID string) {
	t.Helper()
	var credentialStatus string
	var endedAt *time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT status, ended_at FROM credentials WHERE machine_id = $1`, machineID).
		Scan(&credentialStatus, &endedAt); err != nil {
		t.Fatalf("read ended credential: %v", err)
	}
	if credentialStatus != "revoked" || endedAt == nil {
		t.Fatalf("credential = (%q, %v), want revoked with ended_at", credentialStatus, endedAt)
	}
}

func dialEndpointMachine(t *testing.T, serverURL, proof string) *websocket.Conn {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/machines/current/connection"
	conn, response, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + proof}},
	})
	if err != nil {
		if response != nil {
			t.Fatalf("open machine WebSocket: %v (HTTP %d)", err, response.StatusCode)
		}
		t.Fatalf("open machine WebSocket: %v", err)
	}
	return conn
}

func readEndpointHello(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read machine hello: %v", err)
	}
}

func assertEndpointConnectionClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != protocol.CloseRevoked {
		t.Fatalf("connection close status = %d (%v), want %d", got, err, protocol.CloseRevoked)
	}
}

func assertEndpointError(t *testing.T, status int, body []byte, wantStatus int, wantError string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("response status = %d, want %d; body = %q", status, wantStatus, body)
	}
	var answer map[string]string
	if err := json.Unmarshal(body, &answer); err != nil || answer["error"] != wantError {
		t.Fatalf("error response = %q, want %s", body, wantError)
	}
}
