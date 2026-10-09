package pairing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/bertpratya/tervi/internal/db/dbtest"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testPublicURL = "https://pair.example:9443/base"

type response struct {
	Error            string `json:"error"`
	Result           string `json:"result"`
	Credential       string `json:"credential"`
	MachineID        string `json:"machine_id"`
	PollingKey       string `json:"polling_key"`
	ApprovalURL      string `json:"approval_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	Status           string `json:"status"`
	Hostname         string `json:"hostname"`
	OSName           string `json:"os_name"`
	OSVersion        string `json:"os_version"`
	AlreadyDecided   bool   `json:"already_decided"`
	PairingCode      string `json:"pairing_code"`
	TriesLeft        *int   `json:"tries_left"`
	FailureReason    string `json:"failure_reason"`
	DisplayName      string `json:"display_name"`
}

type testAPI struct {
	pool    *pgxpool.Pool
	handler http.Handler
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	pool := dbtest.New(t)
	mux := http.NewServeMux()
	Register(mux, Deps{
		Pool:      pool,
		PublicURL: testPublicURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &testAPI{pool: pool, handler: mux}
}

func (api *testAPI) request(t *testing.T, method, path, body, proof string) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if proof != "" {
		req.Header.Set("Authorization", "Bearer "+proof)
	}
	rec := httptest.NewRecorder()
	api.handler.ServeHTTP(rec, req)
	return rec
}

func (api *testAPI) requestWithHeader(method, path, body, authorization, host string) *httptest.ResponseRecorder {
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	req.Header.Set("Authorization", authorization)
	req.Host = host
	rec := httptest.NewRecorder()
	api.handler.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) response {
	t.Helper()
	var got response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response was not valid JSON: %v", err)
	}
	return got
}

func (api *testAPI) start(t *testing.T, body string) response {
	t.Helper()
	rec := api.request(t, http.MethodPost, "/api/v1/pairings", body, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status = %d, want %d", rec.Code, http.StatusCreated)
	}
	return decodeResponse(t, rec)
}

func approvalKey(t *testing.T, approvalURL string) string {
	t.Helper()
	parsed, err := url.Parse(approvalURL)
	if err != nil {
		t.Fatalf("parse approval URL: %v", err)
	}
	const marker = "/pair/"
	index := strings.LastIndex(parsed.Path, marker)
	if index < 0 {
		t.Fatalf("approval URL path does not use the pair route")
	}
	key := parsed.Path[index+len(marker):]
	if strings.Contains(key, "/") {
		t.Fatalf("approval URL does not contain one key segment")
	}
	return key
}

func (api *testAPI) startKnown(t *testing.T) (response, string) {
	t.Helper()
	started := api.start(t, `{"hostname":"bert-desktop","os_name":"Ubuntu","os_version":"26.04"}`)
	return started, approvalKey(t, started.ApprovalURL)
}

func pairingCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pairing_requests`).Scan(&count); err != nil {
		t.Fatalf("count pairing requests: %v", err)
	}
	return count
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d", rec.Code, status)
	}
	got := decodeResponse(t, rec)
	if got.Error != code {
		t.Fatalf("error = %q, want %q", got.Error, code)
	}
}

func TestStartReturnsKeyLinkAndTimeLeft(t *testing.T) {
	api := newTestAPI(t)
	got := api.start(t, `{}`)
	if len(got.PollingKey) != 43 {
		t.Errorf("polling key length = %d, want 43", len(got.PollingKey))
	}
	key := approvalKey(t, got.ApprovalURL)
	if len(key) != 43 {
		t.Errorf("approval key length = %d, want 43", len(key))
	}
	if got.ApprovalURL != testPublicURL+"/pair/"+key {
		t.Errorf("approval URL does not use the configured public URL")
	}
	if got.ExpiresInSeconds != 600 {
		t.Errorf("expires_in_seconds = %d, want 600", got.ExpiresInSeconds)
	}
}

func TestStartStoresOnlyHashes(t *testing.T) {
	api := newTestAPI(t)
	got := api.start(t, `{"hostname":"desktop","os_name":"Linux","os_version":"1"}`)
	approval := approvalKey(t, got.ApprovalURL)
	var pollingHash, approvalHash []byte
	var columns [12]string
	err := api.pool.QueryRow(context.Background(), `SELECT polling_key_hash, approval_key_hash,
		hostname, os_name, os_version, status, encode(polling_key_hash, 'hex'),
		encode(approval_key_hash, 'hex'), coalesce(pairing_code, ''), coalesce(failure_reason, ''),
		id::text, tries_left::text, created_at::text, expires_at::text
		FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(got.PollingKey))).Scan(
		&pollingHash, &approvalHash,
		&columns[0], &columns[1], &columns[2], &columns[3], &columns[4], &columns[5],
		&columns[6], &columns[7], &columns[8], &columns[9], &columns[10], &columns[11])
	if err != nil {
		t.Fatalf("read started pairing: %v", err)
	}
	if !bytes.Equal(pollingHash, secret.Hash(secret.FromString(got.PollingKey))) {
		t.Error("stored polling key hash does not match the returned key")
	}
	if !bytes.Equal(approvalHash, secret.Hash(secret.FromString(approval))) {
		t.Error("stored approval key hash does not match the URL key")
	}
	for _, value := range []string{got.PollingKey, approval} {
		for _, column := range columns {
			if strings.Contains(column, value) {
				t.Error("a raw key appears in a stored pairing column")
			}
		}
	}
}

func TestStartKeysAreUnique(t *testing.T) {
	api := newTestAPI(t)
	first := api.start(t, `{}`)
	second := api.start(t, `{}`)
	if first.PollingKey == second.PollingKey {
		t.Error("two starts returned the same polling key")
	}
	if approvalKey(t, first.ApprovalURL) == approvalKey(t, second.ApprovalURL) {
		t.Error("two starts returned the same approval key")
	}
}

func TestStartIgnoresHostHeader(t *testing.T) {
	api := newTestAPI(t)
	rec := api.requestWithHeader(http.MethodPost, "/api/v1/pairings", `{}`, "", "forged.example")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	got := decodeResponse(t, rec)
	if !strings.HasPrefix(got.ApprovalURL, testPublicURL+"/pair/") {
		t.Error("approval URL was not based on configured public URL")
	}
}

func TestStartLimits(t *testing.T) {
	api := newTestAPI(t)
	valid := map[string]string{
		"hostname":   strings.Repeat("ก", 64),
		"os_name":    strings.Repeat("ข", 64),
		"os_version": strings.Repeat("ค", 32),
	}
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal("marshal valid limits")
	}
	if rec := api.request(t, http.MethodPost, "/api/v1/pairings", string(body), ""); rec.Code != http.StatusCreated {
		t.Fatalf("exact rune limits status = %d, want %d", rec.Code, http.StatusCreated)
	}
	initialCount := pairingCount(t, api.pool)

	for _, field := range []struct {
		name     string
		jsonName string
		value    string
	}{
		{name: "hostname", value: strings.Repeat("ก", 65)},
		{name: "os_name", value: strings.Repeat("ข", 65)},
		{name: "os_version", value: strings.Repeat("ค", 33)},
		{name: "hostname_nul", jsonName: "hostname", value: "bert\x00desktop"},
	} {
		t.Run(field.name, func(t *testing.T) {
			name := field.jsonName
			if name == "" {
				name = field.name
			}
			body, err := json.Marshal(map[string]string{name: field.value})
			if err != nil {
				t.Fatal("marshal over-limit value")
			}
			assertError(t, api.request(t, http.MethodPost, "/api/v1/pairings", string(body), ""), http.StatusBadRequest, "invalid_input")
			if got := pairingCount(t, api.pool); got != initialCount {
				t.Errorf("pairing count = %d after invalid input, want %d", got, initialCount)
			}
		})
	}
	largeBody := `{"padding":"` + strings.Repeat("x", 5*1024) + `"}`
	assertError(t, api.request(t, http.MethodPost, "/api/v1/pairings", largeBody, ""), http.StatusBadRequest, "invalid_input")
	if got := pairingCount(t, api.pool); got != initialCount {
		t.Errorf("pairing count = %d after oversized body, want %d", got, initialCount)
	}
}

func TestStartMissingFields(t *testing.T) {
	api := newTestAPI(t)
	got := api.start(t, `{}`)
	var hostname, osName, osVersion string
	if err := api.pool.QueryRow(context.Background(), `SELECT hostname, os_name, os_version
		FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(got.PollingKey))).
		Scan(&hostname, &osName, &osVersion); err != nil {
		t.Fatalf("read pairing details: %v", err)
	}
	if hostname != "" || osName != "" || osVersion != "" {
		t.Error("missing details were not stored as empty strings")
	}
}

func TestPollWaiting(t *testing.T) {
	api := newTestAPI(t)
	started := api.start(t, `{}`)
	rec := api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeResponse(t, rec)
	if got.Status != "waiting_for_approval" {
		t.Errorf("status = %q, want waiting_for_approval", got.Status)
	}
	if got.ExpiresInSeconds < 595 || got.ExpiresInSeconds > 600 {
		t.Errorf("expires_in_seconds = %d, want 595 through 600", got.ExpiresInSeconds)
	}
}

func TestPollUnknownKey(t *testing.T) {
	api := newTestAPI(t)
	assertError(t, api.request(t, http.MethodGet, "/api/v1/pairings/current", "", "not-a-key"), http.StatusUnauthorized, "unknown_key")
	assertError(t, api.request(t, http.MethodGet, "/api/v1/pairings/current", "", ""), http.StatusUnauthorized, "unknown_key")
	rec := api.requestWithHeader(http.MethodGet, "/api/v1/pairings/current", "", "Basic abc", "")
	assertError(t, rec, http.StatusUnauthorized, "unknown_key")
}

func TestPollAfterExpiry(t *testing.T) {
	api := newTestAPI(t)
	started := api.start(t, `{}`)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET expires_at = now() - interval '1 second'
		WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(started.PollingKey))); err != nil {
		t.Fatalf("expire pairing: %v", err)
	}
	rec := api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeResponse(t, rec)
	if got.Status != "expired" || got.ExpiresInSeconds != 0 {
		t.Errorf("expired poll returned wrong state")
	}
	var storedStatus string
	if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(started.PollingKey))).Scan(&storedStatus); err != nil {
		t.Fatalf("read stored status: %v", err)
	}
	if storedStatus != "waiting_for_approval" {
		t.Errorf("stored status = %q, want waiting_for_approval", storedStatus)
	}
}

func TestReadShowsDetailsAndChangesNothing(t *testing.T) {
	api := newTestAPI(t)
	_, key := api.startKnown(t)
	var beforeRow string
	if err := api.pool.QueryRow(context.Background(), `SELECT row_to_json(p)::text FROM pairing_requests p WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))).Scan(&beforeRow); err != nil {
		t.Fatalf("read pairing before GET: %v", err)
	}
	rec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeResponse(t, rec)
	if got.Status != "waiting_for_approval" || got.Hostname != "bert-desktop" || got.OSName != "Ubuntu" || got.OSVersion != "26.04" {
		t.Error("approval read did not return stored details and waiting state")
	}
	var afterRow string
	if err := api.pool.QueryRow(context.Background(), `SELECT row_to_json(p)::text FROM pairing_requests p WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))).Scan(&afterRow); err != nil {
		t.Fatalf("read pairing after GET: %v", err)
	}
	if beforeRow != afterRow {
		t.Error("approval GET changed the pairing row")
	}
}

func TestAcceptCreatesCode(t *testing.T) {
	api := newTestAPI(t)
	started, key := api.startKnown(t)
	rec := api.request(t, http.MethodPost, "/api/v1/approvals/current/accept", "", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeResponse(t, rec)
	if got.Status != "waiting_for_code" || !regexp.MustCompile(`^\d{4}-\d{4}$`).MatchString(got.PairingCode) {
		t.Error("accept did not return waiting_for_code with a pairing code")
	}
	poll := api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey)
	pollResult := decodeResponse(t, poll)
	if pollResult.Status != "waiting_for_code" || pollResult.TriesLeft == nil || *pollResult.TriesLeft != 5 {
		t.Error("poll after accept did not include waiting_for_code and five tries")
	}
}

func TestReopenShowsSameCode(t *testing.T) {
	api := newTestAPI(t)
	_, key := api.startKnown(t)
	accepted := decodeResponse(t, api.request(t, http.MethodPost, "/api/v1/approvals/current/accept", "", key))
	reopened := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", key)
	if reopened.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", reopened.Code, http.StatusOK)
	}
	got := decodeResponse(t, reopened)
	if got.Status != "waiting_for_code" || got.PairingCode != accepted.PairingCode {
		t.Error("reopening approval did not return the same pairing code")
	}
}

func TestReject(t *testing.T) {
	api := newTestAPI(t)
	started, key := api.startKnown(t)
	rec := api.request(t, http.MethodPost, "/api/v1/approvals/current/reject", "", key)
	if rec.Code != http.StatusOK || decodeResponse(t, rec).Status != "rejected" {
		t.Error("reject did not return rejected")
	}
	poll := api.request(t, http.MethodGet, "/api/v1/pairings/current", "", started.PollingKey)
	if poll.Code != http.StatusOK || decodeResponse(t, poll).Status != "rejected" {
		t.Error("poll after reject did not return rejected")
	}
}

func TestSecondClickDoesNotChangeDecision(t *testing.T) {
	api := newTestAPI(t)
	for _, order := range [][]string{{"accept", "reject"}, {"reject", "accept"}} {
		t.Run(order[0]+"_then_"+order[1], func(t *testing.T) {
			_, key := api.startKnown(t)
			first := api.request(t, http.MethodPost, "/api/v1/approvals/current/"+order[0], "", key)
			firstState := decodeResponse(t, first)
			second := api.request(t, http.MethodPost, "/api/v1/approvals/current/"+order[1], "", key)
			secondState := decodeResponse(t, second)
			wantStatus := "waiting_for_code"
			if order[0] == "reject" {
				wantStatus = "rejected"
			}
			if firstState.Status != wantStatus || secondState.Status != wantStatus || !secondState.AlreadyDecided {
				t.Error("second approval click changed or misreported the first decision")
			}
			if secondState.PairingCode != firstState.PairingCode {
				t.Error("second approval click changed the stored pairing code")
			}
		})
	}
}

func TestSimultaneousClicks(t *testing.T) {
	api := newTestAPI(t)
	for round := 0; round < 50; round++ {
		_, key := api.startKnown(t)
		start := make(chan struct{})
		type result struct {
			action string
			status int
			body   response
		}
		results := make(chan result, 2)
		var workers sync.WaitGroup
		for _, action := range []string{"accept", "reject"} {
			workers.Add(1)
			go func(action string) {
				defer workers.Done()
				<-start
				req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/current/"+action, nil)
				req.Header.Set("Authorization", "Bearer "+key)
				rec := httptest.NewRecorder()
				api.handler.ServeHTTP(rec, req)
				var got response
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				results <- result{action: action, status: rec.Code, body: got}
			}(action)
		}
		close(start)
		workers.Wait()
		close(results)
		var firstWinner *result
		var alreadyDecided int
		for got := range results {
			if got.status != http.StatusOK {
				t.Fatalf("round %d %s status = %d, want %d", round, got.action, got.status, http.StatusOK)
			}
			if !got.body.AlreadyDecided {
				if firstWinner != nil {
					t.Fatalf("round %d had more than one winning click", round)
				}
				copy := got
				firstWinner = &copy
			} else {
				alreadyDecided++
			}
		}
		if firstWinner == nil || alreadyDecided != 1 {
			t.Fatalf("round %d did not have exactly one winning click", round)
		}
		wantStatus := "waiting_for_code"
		if firstWinner.action == "reject" {
			wantStatus = "rejected"
		}
		if firstWinner.body.Status != wantStatus {
			t.Fatalf("round %d winner state does not match its action", round)
		}
		var storedStatus string
		if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))).Scan(&storedStatus); err != nil {
			t.Fatalf("read round %d decision: %v", round, err)
		}
		if storedStatus != wantStatus {
			t.Fatalf("round %d stored state does not match winning click", round)
		}
	}
}

func TestExpiredCannotBeDecided(t *testing.T) {
	api := newTestAPI(t)
	for _, action := range []string{"accept", "reject"} {
		t.Run(action, func(t *testing.T) {
			_, key := api.startKnown(t)
			if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET expires_at = now() - interval '1 second'
				WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))); err != nil {
				t.Fatalf("expire pairing: %v", err)
			}
			rec := api.request(t, http.MethodPost, "/api/v1/approvals/current/"+action, "", key)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			got := decodeResponse(t, rec)
			if got.Status != "expired" || got.AlreadyDecided {
				t.Error("expired click was not reported as expired and undecided")
			}
			if _, present := responseField(t, rec.Body.Bytes(), "pairing_code"); present {
				t.Error("expired response included a pairing code")
			}
			var storedStatus string
			if err := api.pool.QueryRow(context.Background(), `SELECT status FROM pairing_requests WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))).Scan(&storedStatus); err != nil {
				t.Fatalf("read stored status: %v", err)
			}
			if storedStatus != "waiting_for_approval" {
				t.Error("expired click changed the stored status")
			}
		})
	}
}

func TestInvalidLink(t *testing.T) {
	api := newTestAPI(t)
	paths := []struct {
		name   string
		method string
		path   string
	}{
		{name: "read", method: http.MethodGet, path: "/api/v1/approvals/current"},
		{name: "accept", method: http.MethodPost, path: "/api/v1/approvals/current/accept"},
		{name: "reject", method: http.MethodPost, path: "/api/v1/approvals/current/reject"},
	}
	for _, route := range paths {
		t.Run(route.name, func(t *testing.T) {
			for _, authorization := range []string{"Bearer not-a-key", "", "Basic not-a-key", "Bearer", "Bearer one two"} {
				t.Run(fmt.Sprintf("header_%d", len(authorization)), func(t *testing.T) {
					assertError(t, api.requestWithHeader(route.method, route.path, "", authorization, ""), http.StatusNotFound, "invalid_link")
				})
			}
		})
	}
	if got := pairingCount(t, api.pool); got != 0 {
		t.Errorf("pairing request count = %d after invalid approval requests, want 0", got)
	}
}

func TestFailedShowsReason(t *testing.T) {
	api := newTestAPI(t)
	_, key := api.startKnown(t)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET status = 'failed', failure_reason = 'wrong_codes'
		WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))); err != nil {
		t.Fatalf("fail pairing: %v", err)
	}
	rec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", key)
	got := decodeResponse(t, rec)
	if rec.Code != http.StatusOK || got.Status != "failed" || got.FailureReason != "wrong_codes" {
		t.Error("failed approval read did not return its failure reason")
	}
	if _, present := responseField(t, rec.Body.Bytes(), "pairing_code"); present {
		t.Error("failed response included a pairing code")
	}
}

func TestOldRejectedNeverExpires(t *testing.T) {
	api := newTestAPI(t)
	_, key := api.startKnown(t)
	if _, err := api.pool.Exec(context.Background(), `UPDATE pairing_requests SET status = 'rejected', expires_at = now() - interval '1 second'
		WHERE approval_key_hash = $1`, secret.Hash(secret.FromString(key))); err != nil {
		t.Fatalf("mark pairing rejected: %v", err)
	}
	rec := api.request(t, http.MethodGet, "/api/v1/approvals/current", "", key)
	if rec.Code != http.StatusOK || decodeResponse(t, rec).Status != "rejected" {
		t.Error("old rejected pairing was treated as expired")
	}
}

func responseField(t *testing.T, body []byte, field string) (json.RawMessage, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("response was not valid JSON: %v", err)
	}
	value, ok := fields[field]
	return value, ok
}
