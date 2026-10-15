package pairing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/coder/websocket"
)

const recognitionIdentity = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type historicalCredentialFixture struct {
	status    string
	createdAt time.Time
	endedAt   time.Time
}

type historicalMachineFixture struct {
	machineID string
	proofs    []string
}

func recognitionIdentityBytes(t *testing.T) []byte {
	t.Helper()
	identity, err := hex.DecodeString(recognitionIdentity)
	if err != nil {
		t.Fatal("decode test identity")
	}
	return identity
}

func startWithIdentity(t *testing.T, api *testAPI, hostname string) (response, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"hostname":      hostname,
		"os_name":       "New OS",
		"os_version":    "9.1",
		"identity_hash": recognitionIdentity,
	})
	if err != nil {
		t.Fatal("marshal start request")
	}
	started := api.start(t, string(body))
	return started, approvalKey(t, started.ApprovalURL)
}

func seedHistoricalMachine(t *testing.T, api *testAPI, displayName string, identity []byte, credentials ...historicalCredentialFixture) historicalMachineFixture {
	t.Helper()
	var machineID string
	if err := api.pool.QueryRow(context.Background(), `INSERT INTO machines
		(hostname, os_name, os_version, display_name, identity_hash)
		VALUES ($1, 'Old OS', '1.0', $1, $2) RETURNING machine_id::text`, displayName, identity).
		Scan(&machineID); err != nil {
		t.Fatalf("insert historical machine: %v", err)
	}
	fixture := historicalMachineFixture{machineID: machineID}
	for _, credentialFixture := range credentials {
		createdAt := credentialFixture.createdAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC().Add(-time.Hour)
		}
		endedAt := credentialFixture.endedAt
		requestStatus := "paired"
		if credentialFixture.status == "failed" || credentialFixture.status == "expired" {
			requestStatus = "failed"
		}
		pollingKey, err := secret.NewKey()
		if err != nil {
			t.Fatal("create fixture polling key")
		}
		approvalKey, err := secret.NewKey()
		if err != nil {
			t.Fatal("create fixture approval key")
		}
		var requestID string
		if err := api.pool.QueryRow(context.Background(), `INSERT INTO pairing_requests
			(status, polling_key_hash, approval_key_hash, expires_at)
			VALUES ($1, $2, $3, now() + interval '1 hour') RETURNING id::text`,
			requestStatus, secret.Hash(pollingKey), secret.Hash(approvalKey)).Scan(&requestID); err != nil {
			t.Fatalf("insert historical pairing request: %v", err)
		}
		proof, err := secret.NewKey()
		if err != nil {
			t.Fatal("create fixture credential")
		}
		if credentialFixture.status == "revoked" && endedAt.IsZero() {
			endedAt = createdAt.Add(time.Hour)
		}
		if _, err := api.pool.Exec(context.Background(), `INSERT INTO credentials
			(machine_id, pairing_request_id, credential_hash, status, created_at, expires_at, ended_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, machineID, requestID, secret.Hash(proof),
			credentialFixture.status, createdAt, time.Now().UTC().Add(time.Hour*24), nullableTime(endedAt)); err != nil {
			t.Fatalf("insert historical credential: %v", err)
		}
		fixture.proofs = append(fixture.proofs, proof.Reveal())
	}
	return fixture
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func matchedMachineID(t *testing.T, api *testAPI, pollingKey string) string {
	t.Helper()
	var machineID string
	if err := api.pool.QueryRow(context.Background(), `SELECT coalesce(matched_machine_id::text, '')
		FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(pollingKey))).
		Scan(&machineID); err != nil {
		t.Fatalf("read matched machine: %v", err)
	}
	return machineID
}

func acceptAndGetCode(t *testing.T, api *testAPI, approval string) string {
	t.Helper()
	rec := api.request(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approval)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want %d", rec.Code, http.StatusOK)
	}
	code := decodeResponse(t, rec).PairingCode
	if code == "" {
		t.Fatal("accept response omitted pairing code")
	}
	return code
}

func credentialStatus(t *testing.T, api *testAPI, proof string) string {
	t.Helper()
	var status string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM credentials WHERE credential_hash = $1`,
		secret.Hash(secret.FromString(proof))).Scan(&status); err != nil {
		t.Fatalf("read credential status: %v", err)
	}
	return status
}

func machineDetails(t *testing.T, api *testAPI, machineID string) (hostname, osName, osVersion, displayName string) {
	t.Helper()
	if err := api.pool.QueryRow(context.Background(), `SELECT hostname, os_name, os_version, display_name
		FROM machines WHERE machine_id = $1`, machineID).Scan(&hostname, &osName, &osVersion, &displayName); err != nil {
		t.Fatalf("read machine details: %v", err)
	}
	return hostname, osName, osVersion, displayName
}

func testMachineConnection(t *testing.T, api *testAPI, proof string) (*httptest.Server, *websocket.Conn) {
	t.Helper()
	server := httptest.NewServer(api.handler)
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/machines/current/connection"
	conn, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + proof}},
	})
	if err != nil {
		t.Fatalf("open machine connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read machine hello: %v", err)
	}
	return server, conn
}

func TestStartStoresIdentity(t *testing.T) {
	api := newTestAPI(t)
	started, approval := startWithIdentity(t, api, "identity-client")
	var stored []byte
	if err := api.pool.QueryRow(context.Background(), `SELECT identity_hash FROM pairing_requests
		WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(started.PollingKey))).Scan(&stored); err != nil {
		t.Fatalf("read stored identity hash: %v", err)
	}
	want := recognitionIdentityBytes(t)
	if len(stored) != 32 || !strings.EqualFold(fmt.Sprintf("%x", stored), recognitionIdentity) {
		t.Errorf("stored identity hash = %x (%d bytes), want the submitted 32-byte hash", stored, len(stored))
	}
	if !equalBytes(stored, want) {
		t.Error("stored identity hash bytes differ from the submitted value")
	}
	code := acceptAndGetCode(t, api, approval)
	newCredential := acceptedCredential(t, api, started.PollingKey, code)
	var machineIdentity []byte
	if err := api.pool.QueryRow(context.Background(), `SELECT identity_hash FROM machines WHERE machine_id = $1`, newCredential.MachineID).Scan(&machineIdentity); err != nil {
		t.Fatalf("read new machine identity hash: %v", err)
	}
	if !equalBytes(machineIdentity, want) {
		t.Errorf("new machine identity hash = %x, want submitted 32-byte hash", machineIdentity)
	}

	withoutIdentity := api.start(t, `{}`)
	var isNull bool
	if err := api.pool.QueryRow(context.Background(), `SELECT identity_hash IS NULL FROM pairing_requests
		WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(withoutIdentity.PollingKey))).Scan(&isNull); err != nil {
		t.Fatalf("read absent identity hash: %v", err)
	}
	if !isNull {
		t.Error("pairing without an identity stored a non-null hash")
	}
	noIdentityApproval := approvalKey(t, withoutIdentity.ApprovalURL)
	noIdentityCode := acceptAndGetCode(t, api, noIdentityApproval)
	noIdentityCredential := acceptedCredential(t, api, withoutIdentity.PollingKey, noIdentityCode)
	if err := api.pool.QueryRow(context.Background(), `SELECT identity_hash IS NULL FROM machines WHERE machine_id = $1`, noIdentityCredential.MachineID).Scan(&isNull); err != nil {
		t.Fatalf("read machine with absent identity: %v", err)
	}
	if !isNull {
		t.Error("machine from pairing without an identity stored a non-null hash")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStartRejectsBadIdentity(t *testing.T) {
	api := newTestAPI(t)
	badValues := []struct {
		name  string
		value string
	}{
		{name: "uppercase", value: strings.ToUpper(recognitionIdentity)},
		{name: "63 characters", value: recognitionIdentity[:63]},
		{name: "65 characters", value: recognitionIdentity + "0"},
		{name: "non-hex", value: strings.Repeat("g", 64)},
	}
	for _, test := range badValues {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"identity_hash": test.value})
			if err != nil {
				t.Fatal("marshal bad identity")
			}
			assertError(t, api.request(t, http.MethodPost, "/api/v1/pairings", string(body), ""), http.StatusBadRequest, "invalid_input")
			if strings.Contains(api.logOutput.String(), test.value) {
				t.Errorf("rejected identity value appeared in logs: %q", test.value)
			}
		})
	}
	assertError(t, api.request(t, http.MethodPost, "/api/v1/pairings", `{"identity_hash":123}`, ""), http.StatusBadRequest, "invalid_input")
	if strings.Contains(api.logOutput.String(), "123") {
		t.Error("rejected numeric identity appeared in logs")
	}
}

func TestMatchPairedBefore(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    string
		shouldHit bool
	}{
		{name: "active credential", status: "active", shouldHit: true},
		{name: "revoked credential", status: "revoked", shouldHit: true},
		{name: "only failed credential", status: "failed"},
		{name: "only expired credential", status: "expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := newTestAPI(t)
			machine := seedHistoricalMachine(t, api, "earlier", recognitionIdentityBytes(t), historicalCredentialFixture{status: test.status})
			started, _ := startWithIdentity(t, api, "renamed-computer")
			got := matchedMachineID(t, api, started.PollingKey)
			if test.shouldHit && got != machine.machineID {
				t.Errorf("matched machine = %q, want %q", got, machine.machineID)
			}
			if !test.shouldHit && got != "" {
				t.Errorf("matched machine = %q, want none", got)
			}
		})
	}

	t.Run("null identity matches nothing", func(t *testing.T) {
		api := newTestAPI(t)
		seedHistoricalMachine(t, api, "unidentified", nil, historicalCredentialFixture{status: "active"})
		started := api.start(t, `{}`)
		if got := matchedMachineID(t, api, started.PollingKey); got != "" {
			t.Errorf("matched machine without an identity = %q, want none", got)
		}
	})
}

func TestMatchMostRecent(t *testing.T) {
	api := newTestAPI(t)
	identity := recognitionIdentityBytes(t)
	first := seedHistoricalMachine(t, api, "old-name", identity,
		historicalCredentialFixture{status: "revoked", createdAt: time.Date(2025, time.March, 10, 0, 0, 0, 0, time.UTC)},
	)
	second := seedHistoricalMachine(t, api, "different-name", identity,
		historicalCredentialFixture{status: "revoked", createdAt: time.Date(2025, time.January, 10, 0, 0, 0, 0, time.UTC)},
		historicalCredentialFixture{status: "failed", createdAt: time.Date(2026, time.June, 10, 0, 0, 0, 0, time.UTC)},
		historicalCredentialFixture{status: "expired", createdAt: time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)},
	)
	started, _ := startWithIdentity(t, api, "current-renamed-name")
	if got := matchedMachineID(t, api, started.PollingKey); got != first.machineID {
		t.Errorf("matched machine = %q, want first-inserted, most recently paired %q (not %q)", got, first.machineID, second.machineID)
	}
}

func TestMatchDecidedOnce(t *testing.T) {
	t.Run("later machine does not become the match", func(t *testing.T) {
		api := newTestAPI(t)
		started, approval := startWithIdentity(t, api, "first")
		machine := seedHistoricalMachine(t, api, "paired-later", recognitionIdentityBytes(t), historicalCredentialFixture{status: "active"})
		if got := matchedMachineID(t, api, started.PollingKey); got != "" {
			t.Errorf("match changed to %q after pairing started; want no stored match (machine %q)", got, machine.machineID)
		}
		approvalRec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval)
		approvalBody := decodeResponse(t, approvalRec)
		if approvalRec.Code != http.StatusOK {
			t.Fatalf("read approval after a later machine appeared: status=%d body=%s", approvalRec.Code, approvalRec.Body.String())
		}
		if _, present := responseField(t, approvalRec.Body.Bytes(), "earlier_machine"); present {
			t.Errorf("approval after a later machine appeared included earlier_machine: %s", approvalRec.Body.String())
		}
		if approvalBody.EarlierMachine != nil {
			t.Errorf("approval after a later machine appeared decoded earlier_machine=%+v; want no match", approvalBody.EarlierMachine)
		}
		code := acceptAndGetCode(t, api, approval)
		newCredential := acceptedCredential(t, api, started.PollingKey, code)
		if newCredential.MachineID == machine.machineID {
			t.Errorf("credential attached to machine paired later (%q); want a new machine because start had no match", machine.machineID)
		}
		if got := machineCount(t, api); got != 2 {
			t.Errorf("machine count = %d, want 2 (later machine and new unmatched machine)", got)
		}
	})

	t.Run("later newer match does not replace the original match", func(t *testing.T) {
		api := newTestAPI(t)
		identity := recognitionIdentityBytes(t)
		earlier := seedHistoricalMachine(t, api, "earlier-a", identity,
			historicalCredentialFixture{status: "active", createdAt: time.Date(2025, time.January, 10, 0, 0, 0, 0, time.UTC)},
		)
		started, approval := startWithIdentity(t, api, "current")
		later := seedHistoricalMachine(t, api, "later-b", identity,
			historicalCredentialFixture{status: "active", createdAt: time.Date(2025, time.March, 10, 0, 0, 0, 0, time.UTC)},
		)
		if got := matchedMachineID(t, api, started.PollingKey); got != earlier.machineID {
			t.Fatalf("stored match = %q, want earlier machine A %q (newer machine B is %q)", got, earlier.machineID, later.machineID)
		}
		approvalRec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval)
		approvalBody := decodeResponse(t, approvalRec)
		if approvalRec.Code != http.StatusOK || approvalBody.EarlierMachine == nil || approvalBody.EarlierMachine.DisplayName != "earlier-a" {
			t.Fatalf("approval after machine B appeared = %d %s; want earlier machine A", approvalRec.Code, approvalRec.Body.String())
		}
		code := acceptAndGetCode(t, api, approval)
		newCredential := acceptedCredential(t, api, started.PollingKey, code)
		if newCredential.MachineID != earlier.machineID {
			t.Errorf("credential attached to %q, want original match A %q (newer B is %q)", newCredential.MachineID, earlier.machineID, later.machineID)
		}
	})
}

func TestApprovalShowsEarlierMachine(t *testing.T) {
	expectedEndedAt := time.Date(2026, time.October, 13, 9, 12, 44, 0, time.UTC)
	for _, test := range []struct {
		name        string
		status      string
		endedAt     time.Time
		stillPaired bool
	}{
		{name: "unpaired", status: "revoked", endedAt: expectedEndedAt},
		{name: "still paired", status: "active", stillPaired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := newTestAPI(t)
			seedHistoricalMachine(t, api, "earlier display", recognitionIdentityBytes(t), historicalCredentialFixture{
				status: test.status, endedAt: test.endedAt,
			})
			started, approval := startWithIdentity(t, api, "current")
			rec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval)
			got := decodeResponse(t, rec)
			if rec.Code != http.StatusOK || got.EarlierMachine == nil {
				t.Fatalf("approval response omitted earlier_machine: status=%d body=%s", rec.Code, rec.Body.String())
			}
			earlier := got.EarlierMachine
			if earlier.DisplayName != "earlier display" || earlier.StillPaired != test.stillPaired {
				t.Errorf("earlier machine = %+v, want name earlier display and still_paired %t", earlier, test.stillPaired)
			}
			if test.stillPaired {
				if earlier.UnpairedAt != nil {
					t.Errorf("still paired machine unpaired_at = %s, want null", earlier.UnpairedAt)
				}
			} else if earlier.UnpairedAt == nil || !earlier.UnpairedAt.Equal(expectedEndedAt) {
				t.Errorf("unpaired_at = %v, want %s", earlier.UnpairedAt, expectedEndedAt)
			}
			if gotID := matchedMachineID(t, api, started.PollingKey); gotID == "" {
				t.Fatal("approval fixture did not store a match")
			}
			rejected := api.request(t, http.MethodPost, "/api/v1/approvals/current/reject", "", approval)
			rejectedBody := decodeResponse(t, rejected)
			if rejected.Code != http.StatusOK || rejectedBody.Status != "rejected" || rejectedBody.EarlierMachine == nil {
				t.Errorf("reject response did not retain the matched machine: %d %s", rejected.Code, rejected.Body.String())
			}
		})
	}

	t.Run("absent without match", func(t *testing.T) {
		api := newTestAPI(t)
		started := api.start(t, `{}`)
		approval := approvalKey(t, started.ApprovalURL)
		rec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval)
		if _, present := responseField(t, rec.Body.Bytes(), "earlier_machine"); present {
			t.Errorf("approval without a match included earlier_machine: %s", rec.Body.String())
		}
	})

	t.Run("accept answer includes earlier machine", func(t *testing.T) {
		api := newTestAPI(t)
		seedHistoricalMachine(t, api, "accepted earlier display", recognitionIdentityBytes(t), historicalCredentialFixture{status: "revoked"})
		started, approval := startWithIdentity(t, api, "current")
		rec := api.request(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approval)
		got := decodeResponse(t, rec)
		if rec.Code != http.StatusOK || got.Status != "waiting_for_code" || got.EarlierMachine == nil {
			t.Fatalf("accept answer omitted earlier_machine: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if got.EarlierMachine.DisplayName != "accepted earlier display" {
			t.Errorf("accept earlier_machine display_name = %q, want accepted earlier display", got.EarlierMachine.DisplayName)
		}
		if matchedMachineID(t, api, started.PollingKey) == "" {
			t.Fatal("accept fixture did not store a match")
		}
	})
}

func TestCodeAttachesToMatchedMachine(t *testing.T) {
	api := newTestAPI(t)
	identity := recognitionIdentityBytes(t)
	prior := seedHistoricalMachine(t, api, "earlier", identity, historicalCredentialFixture{status: "active"})
	started, approval := startWithIdentity(t, api, "renamed")
	code := acceptAndGetCode(t, api, approval)
	newCredential := acceptedCredential(t, api, started.PollingKey, code)
	if newCredential.MachineID != prior.machineID {
		t.Errorf("new credential machine_id = %q, want matched machine %q", newCredential.MachineID, prior.machineID)
	}
	if got := machineCount(t, api); got != 1 {
		t.Errorf("machine count = %d, want 1", got)
	}
	if got := credentialStatus(t, api, newCredential.Credential); got != "pending" {
		t.Errorf("new credential status = %q, want pending", got)
	}
	if got := credentialStatus(t, api, prior.proofs[0]); got != "active" {
		t.Errorf("earlier credential status = %q before acknowledgment, want active", got)
	}
}

func TestAcknowledgeBringsMachineBack(t *testing.T) {
	api := newTestAPI(t)
	prior := seedHistoricalMachine(t, api, "earlier", recognitionIdentityBytes(t), historicalCredentialFixture{status: "active"})
	started, approval := startWithIdentity(t, api, "current-hostname")
	code := acceptAndGetCode(t, api, approval)
	newCredential := acceptedCredential(t, api, started.PollingKey, code)
	ack := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", newCredential.Credential))
	if ack.Result != "ok" {
		t.Fatalf("acknowledgment result = %q, want ok", ack.Result)
	}
	if got := credentialStatus(t, api, prior.proofs[0]); got != "revoked" {
		t.Errorf("earlier credential status = %q, want revoked", got)
	}
	if got := credentialStatus(t, api, newCredential.Credential); got != "active" {
		t.Errorf("new credential status = %q, want active", got)
	}
	if hostname, osName, osVersion, displayName := machineDetails(t, api, prior.machineID); hostname != "current-hostname" || osName != "New OS" || osVersion != "9.1" || displayName != "current-hostname" {
		t.Errorf("restored machine details = %q/%q/%q/%q, want current-hostname/New OS/9.1/current-hostname", hostname, osName, osVersion, displayName)
	}
	var listed struct {
		Machines []struct {
			MachineID string `json:"machine_id"`
		} `json:"machines"`
	}
	listResponse := api.request(t, http.MethodGet, "/api/v1/machines", "", "")
	if listResponse.Code != http.StatusOK || json.Unmarshal(listResponse.Body.Bytes(), &listed) != nil {
		t.Fatalf("decode machine list response (status %d): %s", listResponse.Code, listResponse.Body.String())
	}
	if len(listed.Machines) != 1 || listed.Machines[0].MachineID != prior.machineID {
		t.Errorf("listed machines = %+v, want matched machine %q once", listed.Machines, prior.machineID)
	}

	_, conn := testMachineConnection(t, api, newCredential.Credential)
	repeat := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", newCredential.Credential))
	if repeat.Result != "ok" || !api.list.Online(prior.machineID) || credentialStatus(t, api, newCredential.Credential) != "active" {
		t.Error("repeated acknowledgment changed the active credential or closed its connection")
	}
	_ = conn
}

func TestAcknowledgeClosesEarlierConnection(t *testing.T) {
	api := newTestAPI(t)
	prior := seedHistoricalMachine(t, api, "earlier", recognitionIdentityBytes(t), historicalCredentialFixture{status: "active"})
	_, conn := testMachineConnection(t, api, prior.proofs[0])
	started, approval := startWithIdentity(t, api, "restored")
	code := acceptAndGetCode(t, api, approval)
	newCredential := acceptedCredential(t, api, started.PollingKey, code)
	ack := api.request(t, http.MethodPost, ackEndpoint, "", newCredential.Credential)
	if ack.Code != http.StatusOK || decodeResponse(t, ack).Result != "ok" {
		t.Fatalf("acknowledgment failed: %d %s", ack.Code, ack.Body.String())
	}
	if got := credentialStatus(t, api, prior.proofs[0]); got != "revoked" {
		t.Fatalf("earlier credential status = %q, want revoked before connection close", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != protocol.CloseRevoked {
		t.Errorf("earlier connection close status = %d (%v), want %d", websocket.CloseStatus(err), err, protocol.CloseRevoked)
	}
}

func TestAcknowledgeSerializesMatchedMachine(t *testing.T) {
	api := newTestAPI(t)
	prior := seedHistoricalMachine(t, api, "earlier", recognitionIdentityBytes(t), historicalCredentialFixture{status: "active"})
	credentials := make([]string, 2)
	for index := range credentials {
		started, approval := startWithIdentity(t, api, fmt.Sprintf("restored-%d", index))
		code := acceptAndGetCode(t, api, approval)
		issued := acceptedCredential(t, api, started.PollingKey, code)
		if issued.MachineID != prior.machineID {
			t.Fatalf("credential %d machine_id = %q, want matched machine %q", index, issued.MachineID, prior.machineID)
		}
		credentials[index] = issued.Credential
	}

	type acknowledgment struct {
		status int
		body   response
	}
	start := make(chan struct{})
	results := make(chan acknowledgment, len(credentials))
	var workers sync.WaitGroup
	for _, proof := range credentials {
		workers.Add(1)
		go func(proof string) {
			defer workers.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, ackEndpoint, nil)
			req.Header.Set("Authorization", "Bearer "+proof)
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)
			results <- acknowledgment{status: rec.Code, body: decodeResponse(t, rec)}
		}(proof)
	}
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result.status != http.StatusOK || result.body.Result != "ok" {
			t.Errorf("concurrent acknowledgment = (%d, %q), want (200, ok)", result.status, result.body.Result)
		}
	}
	var activeCount int
	if err := api.pool.QueryRow(context.Background(), `SELECT count(*) FROM credentials
		WHERE machine_id = $1 AND status = 'active'`, prior.machineID).Scan(&activeCount); err != nil {
		t.Fatalf("count active credentials: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("active credential count = %d, want 1", activeCount)
	}
}

func TestSaveFailureKeepsEarlierCredential(t *testing.T) {
	for _, test := range []struct {
		name string
		fail bool
	}{
		{name: "reported save failure", fail: true},
		{name: "expired credential"},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := newTestAPI(t)
			prior := seedHistoricalMachine(t, api, "earlier", recognitionIdentityBytes(t), historicalCredentialFixture{status: "active"})
			started, approval := startWithIdentity(t, api, "restored")
			code := acceptAndGetCode(t, api, approval)
			newCredential := acceptedCredential(t, api, started.PollingKey, code)
			if test.fail {
				got := decodeResponse(t, api.request(t, http.MethodPost, failEndpoint, "", newCredential.Credential))
				if got.Result != "ok" {
					t.Fatalf("save failure result = %q, want ok", got.Result)
				}
			} else {
				if _, err := api.pool.Exec(context.Background(), `UPDATE credentials SET expires_at = now() - interval '1 second'
					WHERE credential_hash = $1`, secret.Hash(secret.FromString(newCredential.Credential))); err != nil {
					t.Fatalf("expire new credential: %v", err)
				}
				got := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", newCredential.Credential))
				if got.Result != "expired" {
					t.Fatalf("expired acknowledgment result = %q, want expired", got.Result)
				}
			}
			if got := credentialStatus(t, api, prior.proofs[0]); got != "active" {
				t.Errorf("earlier credential status = %q, want active", got)
			}
			if got := machineCount(t, api); got != 1 {
				t.Errorf("machine count after unsuccessful restore = %d, want the original machine only", got)
			}
			if hostname, osName, osVersion, displayName := machineDetails(t, api, prior.machineID); hostname != "earlier" || osName != "Old OS" || osVersion != "1.0" || displayName != "earlier" {
				t.Errorf("failed restore changed earlier machine details: %q/%q/%q/%q", hostname, osName, osVersion, displayName)
			}
		})
	}
}
