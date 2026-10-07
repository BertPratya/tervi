package e2e

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/db/dbtest"
	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/worker/flow"
	"github.com/bertpratya/tervi/internal/worker/local"
)

const (
	acknowledgmentPath = "/api/v1/machines/current/acknowledgment"
	quickWait          = 8 * time.Second
)

type safeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

type testProxy struct {
	mu     sync.Mutex
	target string

	holdAck        bool
	heldAck        chan struct{}
	releaseAck     chan struct{}
	heldAckOnce    sync.Once
	dropAckAnswers int
	ackCount       int
	threeAcks      chan struct{}
	threeAcksOnce  sync.Once

	pollingKey string
	credential string
	client     *http.Client
}

func newTestProxy(t *testing.T) (*testProxy, *httptest.Server) {
	t.Helper()
	p := &testProxy{
		heldAck:    make(chan struct{}),
		releaseAck: make(chan struct{}),
		threeAcks:  make(chan struct{}),
		client:     &http.Client{Timeout: 3 * time.Second},
	}
	return p, httptest.NewServer(http.HandlerFunc(p.serveHTTP))
}

func (p *testProxy) setTarget(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
}

func (p *testProxy) setHoldAck(hold bool) {
	p.mu.Lock()
	p.holdAck = hold
	p.mu.Unlock()
}

func (p *testProxy) dropNextAckAnswers(count int) {
	p.mu.Lock()
	p.dropAckAnswers = count
	p.mu.Unlock()
}

func (p *testProxy) secrets() (pollingKey, credential string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pollingKey, p.credential
}

func (p *testProxy) ackRequests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ackCount
}

func (p *testProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	target := p.target
	isAck := r.URL.Path == acknowledgmentPath
	holdAck := isAck && p.holdAck
	if isAck {
		p.ackCount++
		if p.ackCount >= 3 {
			p.threeAcksOnce.Do(func() { close(p.threeAcks) })
		}
	}
	p.mu.Unlock()
	if target == "" {
		http.Error(w, "proxy target is not ready", http.StatusBadGateway)
		return
	}
	if holdAck {
		p.heldAckOnce.Do(func() { close(p.heldAck) })
		<-p.releaseAck
		closeDownstream(w)
		return
	}

	base, err := url.Parse(target)
	if err != nil {
		http.Error(w, "invalid proxy target", http.StatusBadGateway)
		return
	}
	outgoing := r.Clone(r.Context())
	outgoing.URL.Scheme = base.Scheme
	outgoing.URL.Host = base.Host
	outgoing.Host = base.Host
	outgoing.RequestURI = ""
	response, err := p.client.Do(outgoing)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		http.Error(w, "upstream response unreadable", http.StatusBadGateway)
		return
	}
	p.recordSecrets(r.URL.Path, body)

	if isAck {
		p.mu.Lock()
		drop := p.dropAckAnswers > 0
		if drop {
			p.dropAckAnswers--
		}
		p.mu.Unlock()
		if drop {
			closeDownstream(w)
			return
		}
	}
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func closeDownstream(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	connection, _, err := hijacker.Hijack()
	if err == nil {
		_ = connection.Close()
	}
}

func (p *testProxy) recordSecrets(path string, body []byte) {
	var response map[string]any
	if json.Unmarshal(body, &response) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch path {
	case "/api/v1/pairings":
		p.pollingKey, _ = response["polling_key"].(string)
	case "/api/v1/pairings/current/code":
		p.credential, _ = response["credential"].(string)
	}
}

type fixture struct {
	serverURL string
	store     *local.Store
	stateDir  string
	proxy     *testProxy
	logs      *safeBuffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.New(t)
	logs := &safeBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	proxy, proxyServer := newTestProxy(t)
	t.Cleanup(proxyServer.Close)
	origin := httptest.NewServer(server.New(server.Config{PublicURL: proxyServer.URL}, pool, logger))
	t.Cleanup(origin.Close)
	proxy.setTarget(origin.URL)
	return &fixture{
		serverURL: proxyServer.URL,
		store:     local.NewStore(local.NewMemoryBackend()),
		stateDir:  filepath.Join(t.TempDir(), "state"),
		proxy:     proxy,
		logs:      logs,
	}
}

type workerRun struct {
	input  *io.PipeWriter
	stdout *safeBuffer
	stderr *safeBuffer
	done   chan int
}

func (f *fixture) startWorker(parent context.Context) *workerRun {
	stdin, input := io.Pipe()
	stdout, stderr := &safeBuffer{}, &safeBuffer{}
	run := &workerRun{input: input, stdout: stdout, stderr: stderr, done: make(chan int, 1)}
	go func() {
		run.done <- local.Run(parent, []string{"pair", "--server", f.serverURL}, local.Env{
			Stdin: stdin, Stdout: stdout, Stderr: stderr, Store: f.store, StateDir: f.stateDir,
		}, flow.New(flow.Options{RequestTimeout: 300 * time.Millisecond, PollInterval: 10 * time.Millisecond}))
		_ = stdin.Close()
	}()
	return run
}

func TestPairingHappyPath(t *testing.T) {
	audit := runHappyPath(t)
	assertNoSecretLeaks(t, audit)
}

func TestRejectEndToEnd(t *testing.T) {
	audit := runReject(t)
	assertNoSecretLeaks(t, audit)
}

func TestFiveWrongCodesEndToEnd(t *testing.T) {
	audit := runFiveWrongCodes(t)
	assertNoSecretLeaks(t, audit)
}

func TestCtrlCAfterSavingThenFinish(t *testing.T) {
	audit := runCtrlCAfterSavingThenFinish(t)
	assertNoSecretLeaks(t, audit)
}

func TestLostAcknowledgmentThenFinish(t *testing.T) {
	audit := runLostAcknowledgmentThenFinish(t)
	assertNoSecretLeaks(t, audit)
}

func TestNoSecretEverPrinted(t *testing.T) {
	// Run every lifecycle through the same audit so this test covers every
	// worker output and server log produced by the other end-to-end cases.
	audits := []secretAudit{
		runHappyPath(t),
		runReject(t),
		runFiveWrongCodes(t),
		runCtrlCAfterSavingThenFinish(t),
		runLostAcknowledgmentThenFinish(t),
	}
	for _, audit := range audits {
		assertNoSecretLeaks(t, audit)
	}
}

type secretAudit struct {
	approvalKey string
	pairingCode string
	pollingKey  string
	credential  string
	stdout      string
	stderr      string
	logs        string
}

func runHappyPath(t *testing.T) secretAudit {
	t.Helper()
	f := newFixture(t)
	run := f.startWorker(context.Background())
	approvalKey := waitForApprovalKey(t, run.stdout)
	pageResponse, err := http.Get(f.serverURL + "/pair/" + approvalKey)
	if err != nil {
		t.Fatalf("GET approval page: %v", err)
	}
	_ = pageResponse.Body.Close()
	if pageResponse.StatusCode != http.StatusOK {
		t.Fatalf("approval page status = %d, want %d", pageResponse.StatusCode, http.StatusOK)
	}
	approval := approvalRequest(t, f.serverURL, approvalKey, http.MethodGet, "")
	if approval.Status != "waiting_for_approval" {
		t.Fatalf("initial approval status = %q, want waiting_for_approval", approval.Status)
	}
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/accept")
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "waiting_for_code")
	if approval.PairingCode == "" {
		t.Fatal("approval API did not return the pairing code")
	}
	pairingCode := approval.PairingCode
	waitFor(t, run.stdout, "Code: ")
	writeCode(t, run, pairingCode)
	if code := awaitRun(t, run); code != 0 {
		t.Fatalf("worker exit = %d, want 0; stdout=%q stderr=%q", code, run.stdout.String(), run.stderr.String())
	}
	if !strings.Contains(run.stdout.String(), "✓ Paired successfully.") {
		t.Errorf("worker output omitted pairing success: %q", run.stdout.String())
	}
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "paired")
	if approval.DisplayName == "" || approval.DisplayName != approval.Hostname {
		t.Errorf("paired display name = %q, hostname = %q; want the computer name", approval.DisplayName, approval.Hostname)
	}
	if approval.OSName == "" || approval.OSVersion == "" {
		t.Errorf("paired OS details = %q %q, want name and version", approval.OSName, approval.OSVersion)
	}
	state, exists, err := local.ReadState(f.stateDir)
	if err != nil || !exists || !state.Confirmed {
		t.Errorf("state after pairing = %#v, exists=%t, error=%v; want confirmed", state, exists, err)
	}
	entry, entryExists, err := f.store.Get()
	if err != nil || !entryExists || entry.Server != f.serverURL || entry.Credential.Reveal() == "" {
		t.Fatalf("secret store entry = %#v, exists=%t, error=%v; want server and credential", entry, entryExists, err)
	}
	assertCredentialAbsentFromFiles(t, f.stateDir, entry.Credential.Reveal())
	audit := f.audit(approvalKey, pairingCode, run)
	if audit.credential == "" || audit.credential != entry.Credential.Reveal() {
		t.Fatal("proxy did not capture the credential returned by the server")
	}
	return audit
}

func runReject(t *testing.T) secretAudit {
	t.Helper()
	f := newFixture(t)
	run := f.startWorker(context.Background())
	approvalKey := waitForApprovalKey(t, run.stdout)
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/reject")
	approval := waitForApprovalStatus(t, f.serverURL, approvalKey, "rejected")
	if code := awaitRun(t, run); code != 1 {
		t.Fatalf("worker exit = %d, want 1; stdout=%q", code, run.stdout.String())
	}
	if !strings.Contains(run.stdout.String(), "✗ Pairing was rejected in the browser.") {
		t.Errorf("worker output omitted rejection: %q", run.stdout.String())
	}
	if approval.Status != "rejected" {
		t.Errorf("approval status = %q, want rejected", approval.Status)
	}
	return f.audit(approvalKey, "", run)
}

func runFiveWrongCodes(t *testing.T) secretAudit {
	t.Helper()
	f := newFixture(t)
	run := f.startWorker(context.Background())
	approvalKey := waitForApprovalKey(t, run.stdout)
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/accept")
	approval := waitForApprovalStatus(t, f.serverURL, approvalKey, "waiting_for_code")
	pairingCode := approval.PairingCode
	waitFor(t, run.stdout, "Code: ")
	wrongCode := "0000-0000"
	if wrongCode == approval.PairingCode {
		wrongCode = "1111-1111"
	}
	for triesLeft := 4; triesLeft > 0; triesLeft-- {
		writeCode(t, run, wrongCode)
		waitFor(t, run.stdout, fmt.Sprintf("Wrong code. %d tries left.", triesLeft))
	}
	writeCode(t, run, wrongCode)
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "failed")
	if code := awaitRun(t, run); code != 1 {
		t.Fatalf("worker exit = %d, want 1; stdout=%q", code, run.stdout.String())
	}
	if !strings.Contains(run.stdout.String(), "✗ Too many wrong codes. Pairing failed.") {
		t.Errorf("worker output omitted failed message: %q", run.stdout.String())
	}
	if approval.FailureReason != "wrong_codes" {
		t.Errorf("approval failure reason = %q, want wrong_codes", approval.FailureReason)
	}
	return f.audit(approvalKey, pairingCode, run)
}

func runCtrlCAfterSavingThenFinish(t *testing.T) secretAudit {
	t.Helper()
	f := newFixture(t)
	f.proxy.setHoldAck(true)
	ctx, cancel := context.WithCancel(context.Background())
	run := f.startWorker(ctx)
	approvalKey := waitForApprovalKey(t, run.stdout)
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/accept")
	approval := waitForApprovalStatus(t, f.serverURL, approvalKey, "waiting_for_code")
	pairingCode := approval.PairingCode
	waitFor(t, run.stdout, "Code: ")
	writeCode(t, run, pairingCode)
	select {
	case <-f.proxy.heldAck:
	case <-time.After(quickWait):
		t.Fatal("proxy did not hold the acknowledgment request")
	}
	entry, exists, err := f.store.Get()
	if err != nil || !exists || entry.Credential.Reveal() == "" {
		t.Fatalf("credential not saved when acknowledgment arrived: exists=%t error=%v", exists, err)
	}
	state, stateExists, err := local.ReadState(f.stateDir)
	if err != nil || !stateExists || !state.CredentialSaved || state.Confirmed {
		t.Fatalf("state while acknowledgment held = %#v, exists=%t, error=%v", state, stateExists, err)
	}
	assertCredentialAbsentFromFiles(t, f.stateDir, entry.Credential.Reveal())
	cancel()
	close(f.proxy.releaseAck)
	if code := awaitRun(t, run); code != 130 {
		t.Fatalf("cancelled worker exit = %d, want 130; stdout=%q", code, run.stdout.String())
	}
	if !strings.Contains(run.stdout.String(), "Pairing cancelled before it was confirmed.") ||
		!strings.Contains(run.stdout.String(), "tervi pair --server "+f.serverURL) {
		t.Errorf("cancel output omitted recovery instructions: %q", run.stdout.String())
	}

	f.proxy.setHoldAck(false)
	resume := f.startWorker(context.Background())
	if code := awaitRun(t, resume); code != 0 {
		t.Fatalf("finish worker exit = %d, want 0; stdout=%q", code, resume.stdout.String())
	}
	if !strings.Contains(resume.stdout.String(), "✓ Paired successfully.") {
		t.Errorf("finish output omitted success: %q", resume.stdout.String())
	}
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "paired")
	return f.audit(approvalKey, pairingCode, run, resume)
}

func runLostAcknowledgmentThenFinish(t *testing.T) secretAudit {
	t.Helper()
	f := newFixture(t)
	f.proxy.dropNextAckAnswers(3)
	run := f.startWorker(context.Background())
	approvalKey := waitForApprovalKey(t, run.stdout)
	_ = approvalRequest(t, f.serverURL, approvalKey, http.MethodPost, "/accept")
	approval := waitForApprovalStatus(t, f.serverURL, approvalKey, "waiting_for_code")
	pairingCode := approval.PairingCode
	waitFor(t, run.stdout, "Code: ")
	writeCode(t, run, pairingCode)
	select {
	case <-f.proxy.threeAcks:
	case <-time.After(quickWait):
		t.Fatal("proxy did not observe three acknowledgment requests")
	}
	if code := awaitRun(t, run); code != 1 {
		t.Fatalf("worker exit = %d, want 1; stdout=%q", code, run.stdout.String())
	}
	if got := f.proxy.ackRequests(); got != 3 {
		t.Errorf("acknowledgment requests = %d, want exactly 3", got)
	}
	for _, want := range []string{
		"Confirming with the server...",
		"No answer, retrying (2 of 3)...",
		"No answer, retrying (3 of 3)...",
		"✗ Saved, but couldn't confirm with the server.",
		"tervi pair --server " + f.serverURL,
	} {
		if !strings.Contains(run.stdout.String(), want) {
			t.Errorf("worker output omitted %q: %q", want, run.stdout.String())
		}
	}
	resume := f.startWorker(context.Background())
	if code := awaitRun(t, resume); code != 0 {
		t.Fatalf("finish worker exit = %d, want 0; stdout=%q", code, resume.stdout.String())
	}
	if !strings.Contains(resume.stdout.String(), "✓ Paired successfully.") {
		t.Errorf("finish output omitted success: %q", resume.stdout.String())
	}
	approval = waitForApprovalStatus(t, f.serverURL, approvalKey, "paired")
	return f.audit(approvalKey, pairingCode, run, resume)
}

type approvalState struct {
	Status        string `json:"status"`
	Hostname      string `json:"hostname"`
	OSName        string `json:"os_name"`
	OSVersion     string `json:"os_version"`
	PairingCode   string `json:"pairing_code"`
	FailureReason string `json:"failure_reason"`
	DisplayName   string `json:"display_name"`
}

func approvalRequest(t *testing.T, serverURL, key, method, suffix string) approvalState {
	t.Helper()
	request, err := http.NewRequest(method, serverURL+"/api/v1/approvals/current"+suffix, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("approval API request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("approval API status = %d, want 200; body=%s", response.StatusCode, body)
	}
	var state approvalState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatalf("decode approval API response: %v", err)
	}
	return state
}

func waitForApprovalStatus(t *testing.T, serverURL, key, wanted string) approvalState {
	t.Helper()
	deadline := time.Now().Add(quickWait)
	for time.Now().Before(deadline) {
		state := approvalRequest(t, serverURL, key, http.MethodGet, "")
		if state.Status == wanted {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state := approvalRequest(t, serverURL, key, http.MethodGet, "")
	t.Fatalf("approval status = %q, want %q", state.Status, wanted)
	return approvalState{}
}

func waitForApprovalKey(t *testing.T, output *safeBuffer) string {
	t.Helper()
	waitFor(t, output, "/pair/")
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimSpace(line)
		parsed, err := url.Parse(line)
		if err == nil && parsed.Path != "" && strings.HasPrefix(parsed.Path, "/pair/") {
			key := strings.TrimPrefix(parsed.Path, "/pair/")
			if key != "" && !strings.Contains(key, "/") {
				return key
			}
		}
	}
	t.Fatalf("approval URL not found in worker output: %q", output.String())
	return ""
}

func waitFor(t *testing.T, output *safeBuffer, text string) {
	t.Helper()
	deadline := time.Now().Add(quickWait)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), text) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("worker output did not contain %q before timeout: %q", text, output.String())
}

func writeCode(t *testing.T, run *workerRun, code string) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(run.input, code+"\n")
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write code to worker: %v", err)
		}
	case <-time.After(quickWait):
		_ = run.input.Close()
		t.Fatalf("worker did not read code within %s", quickWait)
	}
}

func awaitRun(t *testing.T, run *workerRun) int {
	t.Helper()
	select {
	case code := <-run.done:
		_ = run.input.Close()
		return code
	case <-time.After(quickWait):
		_ = run.input.Close()
		t.Fatalf("worker did not exit; stdout=%q stderr=%q", run.stdout.String(), run.stderr.String())
		return -1
	}
}

func (f *fixture) audit(approvalKey, pairingCode string, runs ...*workerRun) secretAudit {
	pollingKey, credential := f.proxy.secrets()
	var stdout, stderr strings.Builder
	for _, run := range runs {
		stdout.WriteString(run.stdout.String())
		stderr.WriteString(run.stderr.String())
	}
	return secretAudit{
		approvalKey: approvalKey,
		pairingCode: pairingCode,
		pollingKey:  pollingKey,
		credential:  credential,
		stdout:      stdout.String(),
		stderr:      stderr.String(),
		logs:        f.logs.String(),
	}
}

func assertNoSecretLeaks(t *testing.T, audit secretAudit) {
	t.Helper()
	if audit.pollingKey == "" {
		t.Error("test did not capture polling key for leak audit")
		return
	}
	secrets := map[string]string{
		"polling key":  audit.pollingKey,
		"pairing code": audit.pairingCode,
		"credential":   audit.credential,
	}
	for name, value := range secrets {
		if value == "" {
			continue
		}
		for streamName, stream := range map[string]string{"stdout": audit.stdout, "stderr": audit.stderr, "server log": audit.logs} {
			if strings.Contains(stream, value) {
				t.Errorf("%s appeared in %s", name, streamName)
			}
		}
	}
	if audit.approvalKey == "" {
		t.Error("test did not capture approval key for leak audit")
		return
	}
	if got := strings.Count(audit.stdout, audit.approvalKey); got != 1 {
		t.Errorf("approval key appeared %d times in worker stdout, want once inside the link", got)
	}
	if !strings.Contains(audit.stdout, "/pair/"+audit.approvalKey) {
		t.Error("approval key was not inside the printed approval link")
	}
	for streamName, stream := range map[string]string{"stderr": audit.stderr, "server log": audit.logs} {
		if strings.Contains(stream, audit.approvalKey) {
			t.Errorf("approval key appeared in %s", streamName)
		}
	}
}

func assertCredentialAbsentFromFiles(t *testing.T, stateDir, credential string) {
	t.Helper()
	err := filepath.WalkDir(stateDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(credential)) {
			t.Errorf("credential was written to local file %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect state directory: %v", err)
	}
}
