package pairing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
)

const (
	codeEndpoint = "/api/v1/pairings/current/code"
	ackEndpoint  = "/api/v1/machines/current/acknowledgment"
	failEndpoint = "/api/v1/machines/current/save-failure"
)

func createCodeReady(t *testing.T, api *testAPI) (response, string, string) {
	t.Helper()
	started, approval := api.startKnown(t)
	accepted := api.request(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approval)
	if accepted.Code != http.StatusOK {
		t.Fatalf("approval status = %d, want %d", accepted.Code, http.StatusOK)
	}
	code := decodeResponse(t, accepted).PairingCode
	if code == "" {
		t.Fatal("approval did not return a pairing code")
	}
	return started, approval, code
}

func submitCode(t *testing.T, api *testAPI, pollingKey, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal("marshal code request")
	}
	return api.request(t, http.MethodPost, codeEndpoint, string(body), pollingKey)
}

func acceptedCredential(t *testing.T, api *testAPI, pollingKey, code string) response {
	t.Helper()
	rec := submitCode(t, api, pollingKey, code)
	if rec.Code != http.StatusOK {
		t.Fatalf("code status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeResponse(t, rec)
	if got.Result != "accepted" {
		t.Fatalf("code result = %q, want accepted", got.Result)
	}
	return got
}

func wrongCode(code string) string {
	for i, r := range code {
		if r >= '0' && r <= '9' {
			replacement := byte('0')
			if r == '0' {
				replacement = '1'
			}
			return code[:i] + string(replacement) + code[i+1:]
		}
	}
	return "0000-0000"
}

func machineCount(t *testing.T, api *testAPI) int {
	t.Helper()
	var count int
	if err := api.pool.QueryRow(context.Background(), `SELECT count(*) FROM machines`).Scan(&count); err != nil {
		t.Fatalf("count machines: %v", err)
	}
	return count
}

func intPointer(value int) *int {
	return &value
}

func machineState(t *testing.T, api *testAPI, machineID string) (string, float64) {
	t.Helper()
	var status string
	var seconds float64
	if err := api.pool.QueryRow(context.Background(), `SELECT status,
		EXTRACT(EPOCH FROM expires_at - now()) FROM machines WHERE machine_id = $1`, machineID).
		Scan(&status, &seconds); err != nil {
		t.Fatalf("read machine state: %v", err)
	}
	return status, seconds
}

func TestCorrectCodeIssuesCredential(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	got := acceptedCredential(t, api, started.PollingKey, code)
	if len(got.Credential) != 43 || got.MachineID == "" {
		t.Fatal("accepted response did not include a credential and machine ID")
	}
	var requestStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&requestStatus); err != nil {
		t.Fatalf("read pairing request status: %v", err)
	}
	if requestStatus != "finishing" {
		t.Errorf("request status = %q, want finishing", requestStatus)
	}
	status, seconds := machineState(t, api, got.MachineID)
	if status != "pending" || seconds < 295 || seconds > 300 {
		t.Errorf("machine status or expiry is incorrect")
	}
}

func TestCodeFormatsAccepted(t *testing.T) {
	for _, format := range []struct {
		name  string
		apply func(string) string
	}{
		{name: "no separator", apply: func(code string) string { return strings.ReplaceAll(code, "-", "") }},
		{name: "space separator", apply: func(code string) string { return strings.Replace(code, "-", " ", 1) }},
	} {
		t.Run(format.name, func(t *testing.T) {
			api := newTestAPI(t)
			started, _, code := createCodeReady(t, api)
			got := acceptedCredential(t, api, started.PollingKey, format.apply(code))
			if len(got.Credential) != 43 {
				t.Errorf("credential length = %d, want 43", len(got.Credential))
			}
		})
	}
}

func TestCredentialOnlyHashed(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	got := acceptedCredential(t, api, started.PollingKey, code)
	var pairingRow, machineRow string
	if err := api.pool.QueryRow(context.Background(), `SELECT to_jsonb(pr)::text FROM pairing_requests pr WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&pairingRow); err != nil {
		t.Fatalf("read pairing row: %v", err)
	}
	if err := api.pool.QueryRow(context.Background(), `SELECT to_jsonb(m)::text FROM machines m WHERE machine_id = $1`, got.MachineID).Scan(&machineRow); err != nil {
		t.Fatalf("read machine row: %v", err)
	}
	if strings.Contains(pairingRow, got.Credential) || strings.Contains(machineRow, got.Credential) {
		t.Error("a database row contains the raw credential")
	}
	var storedHash []byte
	if err := api.pool.QueryRow(context.Background(), `SELECT credential_hash FROM machines WHERE machine_id = $1`, got.MachineID).Scan(&storedHash); err != nil {
		t.Fatalf("read credential hash: %v", err)
	}
	if !bytes.Equal(storedHash, secret.Hash(secret.FromString(got.Credential))) {
		t.Error("stored credential hash does not match the returned credential")
	}
}

func TestNoSecondCredential(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	first := acceptedCredential(t, api, started.PollingKey, code)
	second := submitCode(t, api, started.PollingKey, code)
	got := decodeResponse(t, second)
	if second.Code != http.StatusOK || got.Result != "not_waiting_for_code" || got.Status != "finishing" {
		t.Errorf("second code response was not not_waiting_for_code/finishing")
	}
	if got.Credential != "" || got.MachineID != "" || machineCount(t, api) != 1 || first.MachineID == "" {
		t.Error("second code issued another credential or machine")
	}
}

func TestSimultaneousCorrectCodes(t *testing.T) {
	api := newTestAPI(t)
	for round := 0; round < 20; round++ {
		started, _, code := createCodeReady(t, api)
		start := make(chan struct{})
		results := make(chan response, 2)
		statuses := make(chan int, 2)
		var workers sync.WaitGroup
		for i := 0; i < 2; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				body, _ := json.Marshal(map[string]string{"code": code})
				req := httptest.NewRequest(http.MethodPost, codeEndpoint, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+started.PollingKey)
				rec := httptest.NewRecorder()
				api.handler.ServeHTTP(rec, req)
				var got response
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				statuses <- rec.Code
				results <- got
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		close(statuses)
		accepted := 0
		for status := range statuses {
			if status != http.StatusOK {
				t.Fatalf("round %d response status = %d, want %d", round, status, http.StatusOK)
			}
		}
		for got := range results {
			if got.Result == "accepted" {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("round %d accepted count = %d, want 1", round, accepted)
		}
		if got := machineCount(t, api); got != round+1 {
			t.Fatalf("round %d machine count = %d, want %d", round, got, round+1)
		}
	}
}

func TestWrongCodeCountsDown(t *testing.T) {
	api := newTestAPI(t)
	started, approval, code := createCodeReady(t, api)
	for attempt, want := range []struct {
		result string
		tries  *int
	}{{"wrong_code", intPointer(4)}, {"wrong_code", intPointer(3)}, {"wrong_code", intPointer(2)}, {"wrong_code", intPointer(1)}, {"failed", nil}} {
		rec := submitCode(t, api, started.PollingKey, wrongCode(code))
		got := decodeResponse(t, rec)
		if rec.Code != http.StatusOK || got.Result != want.result || (want.tries == nil && got.TriesLeft != nil) ||
			(want.tries != nil && (got.TriesLeft == nil || *got.TriesLeft != *want.tries)) {
			t.Errorf("attempt %d returned an unexpected result or try count", attempt+1)
		}
	}
	var status, reason string
	var tries int
	if err := api.pool.QueryRow(context.Background(), `SELECT status, failure_reason, tries_left FROM pairing_requests WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&status, &reason, &tries); err != nil {
		t.Fatalf("read failed request: %v", err)
	}
	if status != "failed" || reason != "wrong_codes" || tries != 0 {
		t.Error("wrong-code limit did not persist the failed state")
	}
	read := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval))
	if read.Status != "failed" || read.FailureReason != "wrong_codes" {
		t.Error("approval read did not report wrong_codes failure")
	}
}

func TestSimultaneousWrongCodes(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	wrong := wrongCode(code)
	start := make(chan struct{})
	results := make(chan response, 10)
	statuses := make(chan int, 10)
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			body, _ := json.Marshal(map[string]string{"code": wrong})
			req := httptest.NewRequest(http.MethodPost, codeEndpoint, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+started.PollingKey)
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)
			var got response
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			statuses <- rec.Code
			results <- got
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(statuses)
	wrongCount := 0
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("response status = %d, want %d", status, http.StatusOK)
		}
	}
	for got := range results {
		if got.Result == "wrong_code" || got.Result == "failed" {
			wrongCount++
		} else if got.Result != "not_waiting_for_code" {
			t.Errorf("unexpected simultaneous wrong-code result %q", got.Result)
		}
	}
	var status, reason string
	var tries int
	if err := api.pool.QueryRow(context.Background(), `SELECT status, failure_reason, tries_left FROM pairing_requests WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&status, &reason, &tries); err != nil {
		t.Fatalf("read final request: %v", err)
	}
	if wrongCount != 5 || tries != 0 || status != "failed" || reason != "wrong_codes" {
		t.Error("simultaneous wrong codes did not stop at exactly five tries")
	}
}

func TestCodeOnlyForItsPairing(t *testing.T) {
	api := newTestAPI(t)
	_, _, firstCode := createCodeReady(t, api)
	second, _, _ := createCodeReady(t, api)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET pairing_code = $1 WHERE polling_key_hash = $2`,
		wrongCode(firstCode), secret.Hash(secret.FromString(second.PollingKey))); err != nil {
		t.Fatalf("set test code: %v", err)
	}
	got := decodeResponse(t, submitCode(t, api, second.PollingKey, firstCode))
	if got.Result != "wrong_code" || got.TriesLeft == nil || *got.TriesLeft != 4 {
		t.Error("one pairing's code did not count as wrong for the other pairing")
	}
}

func TestNoCodeWhenFinished(t *testing.T) {
	for _, test := range []struct {
		status     string
		reason     string
		wantResult string
	}{
		{status: "expired", wantResult: "expired"},
		{status: "rejected", wantResult: "not_waiting_for_code"},
		{status: "failed", reason: "wrong_codes", wantResult: "not_waiting_for_code"},
	} {
		t.Run(test.status, func(t *testing.T) {
			api := newTestAPI(t)
			started, _, code := createCodeReady(t, api)
			if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET status = $1,
				failure_reason = CASE WHEN $1 = 'failed' THEN $2 ELSE NULL END WHERE polling_key_hash = $3`,
				test.status, test.reason, secret.Hash(secret.FromString(started.PollingKey))); err != nil {
				t.Fatalf("set terminal status: %v", err)
			}
			got := decodeResponse(t, submitCode(t, api, started.PollingKey, code))
			if got.Result != test.wantResult || (test.wantResult != "expired" && got.Status != test.status) {
				t.Errorf("terminal request returned %q with status %q, want %q with status %q", got.Result, got.Status, test.wantResult, test.status)
			}
			if machineCount(t, api) != 0 {
				t.Error("terminal request created a machine")
			}
		})
	}
}

func TestCodeExpiredWhileWaitingForCode(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests
		SET expires_at = now() - interval '1 second' WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))); err != nil {
		t.Fatalf("expire waiting request: %v", err)
	}
	got := decodeResponse(t, submitCode(t, api, started.PollingKey, code))
	if got.Result != "expired" {
		t.Errorf("expired waiting request result = %q, want expired", got.Result)
	}
	if machineCount(t, api) != 0 {
		t.Error("expired waiting request created a machine")
	}
	var status string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&status); err != nil {
		t.Fatalf("read expired waiting request: %v", err)
	}
	if status != "waiting_for_code" {
		t.Errorf("expired request status = %q, want unchanged waiting_for_code", status)
	}
}

func TestCodeExpiredAfterCleanupPass(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests
		SET expires_at = now() - interval '1 second' WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))); err != nil {
		t.Fatalf("expire waiting request: %v", err)
	}
	if err := cleanupPass(context.Background(), api.pool); err != nil {
		t.Fatalf("run cleanup pass: %v", err)
	}
	var status string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(started.PollingKey))).Scan(&status); err != nil {
		t.Fatalf("read cleaned up request: %v", err)
	}
	if status != "expired" {
		t.Fatalf("cleanup status = %q, want expired", status)
	}
	got := decodeResponse(t, submitCode(t, api, started.PollingKey, code))
	if got.Result != "expired" {
		t.Errorf("code after cleanup result = %q, want expired", got.Result)
	}
	if machineCount(t, api) != 0 {
		t.Error("expired request created a machine")
	}
}

func TestCodeUnavailableUsesPollStatusAfterExpiry(t *testing.T) {
	t.Run("waiting for approval", func(t *testing.T) {
		api := newTestAPI(t)
		started, _ := api.startKnown(t)
		if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests
			SET expires_at = now() - interval '1 second' WHERE polling_key_hash = $1`,
			secret.Hash(secret.FromString(started.PollingKey))); err != nil {
			t.Fatalf("expire waiting request: %v", err)
		}
		got := decodeResponse(t, submitCode(t, api, started.PollingKey, "4827-1934"))
		poll := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey))
		if got.Result != "expired" || got.Status != "" || poll.Status != "expired" {
			t.Errorf("code response %q/%q and poll status %q; want expired and poll status expired", got.Result, got.Status, poll.Status)
		}
	})

	t.Run("finishing machine expired", func(t *testing.T) {
		api := newTestAPI(t)
		started, _, code := createCodeReady(t, api)
		credential := acceptedCredential(t, api, started.PollingKey, code)
		if _, err := api.pool.Exec(context.Background(), `UPDATE machines
			SET expires_at = now() - interval '1 second' WHERE machine_id = $1`, credential.MachineID); err != nil {
			t.Fatalf("expire pending machine: %v", err)
		}
		got := decodeResponse(t, submitCode(t, api, started.PollingKey, code))
		poll := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey))
		if got.Result != "not_waiting_for_code" || got.Status != "failed" || poll.Status != got.Status || poll.FailureReason != "not_confirmed" {
			t.Errorf("code response %q/%q and poll status/reason %q/%q; want not_waiting_for_code/failed with not_confirmed", got.Result, got.Status, poll.Status, poll.FailureReason)
		}
	})
}

func TestOldRejectedAnswersNotWaiting(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET status = 'rejected', expires_at = now() - interval '1 second'
		WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(started.PollingKey))); err != nil {
		t.Fatalf("expire rejected request: %v", err)
	}
	got := decodeResponse(t, submitCode(t, api, started.PollingKey, code))
	if got.Result != "not_waiting_for_code" || got.Status != "rejected" {
		t.Error("old rejected request was treated as expired or accepted a code")
	}
}

func TestAcknowledgment(t *testing.T) {
	api := newTestAPI(t)
	started, approval, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	ack := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", credential.Credential))
	if ack.Result != "ok" {
		t.Errorf("acknowledgment result = %q, want ok", ack.Result)
	}
	var machineStatus, requestStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status FROM machines m JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, credential.MachineID).
		Scan(&machineStatus, &requestStatus); err != nil {
		t.Fatalf("read paired state: %v", err)
	}
	if machineStatus != "active" || requestStatus != "paired" {
		t.Error("acknowledgment did not activate the machine and pair its request")
	}
	read := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval))
	if read.Status != "paired" || read.DisplayName != "bert-desktop" {
		t.Error("approval read did not show the paired computer")
	}
}

func TestAcknowledgmentRepeatable(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	for i := 0; i < 2; i++ {
		got := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", credential.Credential))
		if got.Result != "ok" {
			t.Errorf("acknowledgment %d result = %q, want ok", i+1, got.Result)
		}
	}
	if got := machineCount(t, api); got != 1 {
		t.Errorf("machine count after repeat acknowledgment = %d, want 1", got)
	}
	var machineStatus, requestStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status FROM machines m
		JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, credential.MachineID).
		Scan(&machineStatus, &requestStatus); err != nil {
		t.Fatalf("read state after repeat acknowledgment: %v", err)
	}
	if machineStatus != "active" || requestStatus != "paired" {
		t.Errorf("state after repeat acknowledgment = %s/%s, want active/paired", machineStatus, requestStatus)
	}
}

func TestAcknowledgmentAfterExpiry(t *testing.T) {
	api := newTestAPI(t)
	started, approval, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	if _, err := api.pool.Exec(context.Background(), `UPDATE machines SET expires_at = now() - interval '1 second' WHERE machine_id = $1`, credential.MachineID); err != nil {
		t.Fatalf("expire pending machine: %v", err)
	}
	ack := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", credential.Credential))
	if ack.Result != "expired" {
		t.Errorf("acknowledgment result = %q, want expired", ack.Result)
	}
	var machineStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM machines WHERE machine_id = $1`, credential.MachineID).Scan(&machineStatus); err != nil {
		t.Fatalf("read machine status: %v", err)
	}
	if machineStatus == "active" {
		t.Error("expired machine became active")
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"poll":     api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey),
		"approval": api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval),
	} {
		got := decodeResponse(t, rec)
		if got.Status != "failed" || got.FailureReason != "not_confirmed" {
			t.Errorf("%s read did not show not_confirmed failure", name)
		}
	}
}

func TestSaveFailure(t *testing.T) {
	api := newTestAPI(t)
	started, approval, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	failure := decodeResponse(t, api.request(t, http.MethodPost, failEndpoint, "", credential.Credential))
	if failure.Result != "ok" {
		t.Errorf("save failure result = %q, want ok", failure.Result)
	}
	var machineStatus, requestStatus, reason string
	if err := api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status, pr.failure_reason FROM machines m JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, credential.MachineID).
		Scan(&machineStatus, &requestStatus, &reason); err != nil {
		t.Fatalf("read failed save state: %v", err)
	}
	if machineStatus != "failed" || requestStatus != "failed" || reason != "not_saved" {
		t.Error("saving failure did not fail the machine and request")
	}
	ack := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", credential.Credential))
	if ack.Result != "failed" {
		t.Errorf("acknowledgment after save failure = %q, want failed", ack.Result)
	}
	read := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval))
	if read.Status != "failed" || read.FailureReason != "not_saved" {
		t.Error("approval read did not show not_saved failure")
	}
}

func TestSaveFailureOnActive(t *testing.T) {
	api := newTestAPI(t)
	started, _, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	if got := decodeResponse(t, api.request(t, http.MethodPost, ackEndpoint, "", credential.Credential)); got.Result != "ok" {
		t.Fatalf("acknowledgment result = %q, want ok", got.Result)
	}
	got := decodeResponse(t, api.request(t, http.MethodPost, failEndpoint, "", credential.Credential))
	if got.Result != "active" {
		t.Errorf("save failure on active machine = %q, want active", got.Result)
	}
	var machineStatus, requestStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status FROM machines m JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, credential.MachineID).
		Scan(&machineStatus, &requestStatus); err != nil {
		t.Fatalf("read active state: %v", err)
	}
	if machineStatus != "active" || requestStatus != "paired" {
		t.Error("save failure changed an active machine")
	}
}

func TestNotPairedUntilConfirmed(t *testing.T) {
	api := newTestAPI(t)
	started, approval, code := createCodeReady(t, api)
	credential := acceptedCredential(t, api, started.PollingKey, code)
	poll := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey))
	read := decodeResponse(t, api.request(t, http.MethodGet, "/api/v1/approvals/current", "", approval))
	if poll.Status == "paired" || read.Status == "paired" {
		t.Error("pending machine was shown as paired before acknowledgment")
	}
	var status string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM machines WHERE machine_id = $1`, credential.MachineID).Scan(&status); err != nil {
		t.Fatalf("read pending machine: %v", err)
	}
	if status != "pending" {
		t.Errorf("machine status = %q, want pending", status)
	}
}

func TestCleanupPass(t *testing.T) {
	api := newTestAPI(t)
	oldWaiting, _ := api.startKnown(t)
	freshWaiting, _ := api.startKnown(t)
	oldPendingPairing, _, oldCode := createCodeReady(t, api)
	oldPending := acceptedCredential(t, api, oldPendingPairing.PollingKey, oldCode)
	freshPendingPairing, _, freshCode := createCodeReady(t, api)
	freshPending := acceptedCredential(t, api, freshPendingPairing.PollingKey, freshCode)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET expires_at = now() - interval '1 second' WHERE polling_key_hash = $1`,
		secret.Hash(secret.FromString(oldWaiting.PollingKey))); err != nil {
		t.Fatalf("expire waiting request: %v", err)
	}
	if _, err := api.pool.Exec(context.Background(), `UPDATE machines SET expires_at = now() - interval '1 second' WHERE machine_id = $1`, oldPending.MachineID); err != nil {
		t.Fatalf("expire pending machine: %v", err)
	}
	if err := cleanupPass(context.Background(), api.pool); err != nil {
		t.Fatalf("run cleanup pass: %v", err)
	}
	var oldWaitingStatus, oldMachineStatus, oldPendingStatus, oldPendingReason string
	var freshWaitingStatus, freshMachineStatus, freshPendingStatus string
	_ = api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(oldWaiting.PollingKey))).Scan(&oldWaitingStatus)
	_ = api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status, pr.failure_reason FROM machines m JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, oldPending.MachineID).Scan(&oldMachineStatus, &oldPendingStatus, &oldPendingReason)
	_ = api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(freshWaiting.PollingKey))).Scan(&freshWaitingStatus)
	_ = api.pool.QueryRow(context.Background(), `SELECT m.status, pr.status FROM machines m JOIN pairing_requests pr ON pr.id = m.pairing_request_id WHERE m.machine_id = $1`, freshPending.MachineID).Scan(&freshMachineStatus, &freshPendingStatus)
	if oldWaitingStatus != "expired" || oldMachineStatus != "expired" || oldPendingStatus != "failed" || oldPendingReason != "not_confirmed" {
		t.Error("cleanup did not expire waiting and pending records")
	}
	if freshWaitingStatus != "waiting_for_approval" || freshMachineStatus != "pending" || freshPendingStatus != "finishing" {
		t.Error("cleanup changed fresh records")
	}
}

func TestUnknownCredential(t *testing.T) {
	api := newTestAPI(t)
	for _, path := range []string{ackEndpoint, failEndpoint} {
		rec := api.request(t, http.MethodPost, path, "", "unknown-credential")
		assertError(t, rec, http.StatusUnauthorized, "unknown_credential")
	}
}
