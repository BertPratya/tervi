package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/service"
	workerstate "github.com/bertpratya/tervi/internal/worker/state"
)

func TestDisconnectEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)

	run, code := f.runCommand(t, "disconnect")
	want := "✓ Disconnected. tervi stays in the background but won't connect.\n  Connect again with: tervi connect\n"
	if code != 0 || run.stdout.String() != want {
		t.Errorf("tervi disconnect = (%d, %q), want exit 0 and %q", code, run.stdout.String(), want)
	}
	waitFor(t, f.daemonLogs, "○ Disconnected.", connectionWait)
	if !f.runner.daemonRunning(0) {
		t.Error("daemon stopped after disconnect; want it to stay running")
	}
	if machine := waitForMachineOnline(t, f, paired.machineID, false, connectionWait); machine.LastSeenSecondsAgo == nil {
		t.Error("offline machine omitted last_seen_seconds_ago")
	}
	if code, output := f.status(t); code != 1 || output != "○ Disconnected.\n  Connect with: tervi connect\n" {
		t.Errorf("tervi status while disconnected = (%d, %q)", code, output)
	}
	assertMachinesPageShowsOffline(t, f, paired.machineID)

	// Stop and restart the in-process daemon to model a reboot. The paired
	// state must still prevent a connection until tervi connect is run.
	f.runner.stop(t)
	f.runner.startDaemon(t)
	waitForControlState(t, f.controlClient, "not_connecting", connectionWait)
	waitForOfflineIntervals(t, f, paired.machineID, 3, time.Second)
	if got := strings.Count(f.daemonLogs.String(), "✓ Connected to "+f.serverURL); got != 1 {
		t.Errorf("connected log lines while disconnected and after daemon restart = %d, want 1", got)
	}

	connectRun, connectCode := f.runCommand(t, "connect")
	if connectCode != 0 || !strings.Contains(connectRun.stdout.String(), "✓ Connected.") {
		t.Errorf("tervi connect = (%d, %q), want exit 0 and connected", connectCode, connectRun.stdout.String())
	}
	waitForMachineOnline(t, f, paired.machineID, true, connectionWait)
	waitForLogCount(t, f.daemonLogs, "✓ Connected to "+f.serverURL, 2, connectionWait)
	assertNoSecrets(t, f, paired.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, paired.credential)
}

func TestDisconnectWithoutDaemonEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)
	f.runner.stop(t)

	run, code := f.runCommand(t, "disconnect")
	want := "✓ Disconnected. tervi won't connect, including after the computer restarts.\n  Connect again with: tervi connect\n"
	if code != 0 || run.stdout.String() != want {
		t.Errorf("tervi disconnect without daemon = (%d, %q), want exit 0 and %q", code, run.stdout.String(), want)
	}
	if code, output := f.status(t); code != 1 || output != "○ Disconnected.\n  Connect with: tervi connect\n" {
		t.Errorf("tervi status while disconnected without daemon = (%d, %q)", code, output)
	}

	f.runner.startDaemon(t)
	waitForControlState(t, f.controlClient, "not_connecting", connectionWait)
	waitForOfflineIntervals(t, f, paired.machineID, 3, time.Second)
	assertNoSecrets(t, f, paired.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, paired.credential)
}

func TestUnpairEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)

	run, code := f.runCommand(t, "unpair", "--yes")
	want := "✓ Unpaired. This computer is no longer paired with " + f.serverURL + ".\n"
	if code != 0 || run.stdout.String() != want {
		t.Errorf("tervi unpair = (%d, %q), want exit 0 and %q", code, run.stdout.String(), want)
	}
	if f.runner.daemonRunning(0) {
		t.Error("daemon still runs after unpair")
	}
	if len(decodeLiveMachines(t, f.machineList(t)).Machines) != 0 {
		t.Error("unpaired machine remains in the Machines list")
	}
	assertDatabaseMachineAndCredential(t, f, paired.machineID, "revoked")
	for _, path := range []string{credential.Path(f.stateDir), workerstate.StatePath(f.stateDir), service.ServiceFilePath(f.serviceDir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("unpair left %q (stat error %v)", path, err)
		}
	}
	if !hasBackgroundCall(f.runner.callsSnapshot(), "systemctl", "--user disable --now tervi") {
		t.Error("fake systemd did not see disable --now tervi")
	}
	assertRevokedCredentialRefused(t, f, paired.credential)
	assertNoSecrets(t, f, paired.credential)
	assertCredentialNotInLocalFiles(t, f, paired.credential)
}

func TestRevokeConnectedEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)
	if status := revokeMachineFromPage(t, f, paired.machineID); status != http.StatusNoContent {
		t.Fatalf("Machines page Revoke returned HTTP %d, want 204", status)
	}
	waitFor(t, f.daemonLogs, unpairedDaemonLine(f.serverURL), connectionWait)
	waitForControlState(t, f.controlClient, "not_connecting", connectionWait)
	state := f.stateSnapshot(t)
	if state.Connect || state.StopReason == nil || *state.StopReason != "credential_revoked" {
		t.Errorf("state after connected revoke = %+v, want connect false and credential_revoked", state)
	}
	if !f.runner.daemonRunning(0) {
		t.Error("daemon exited after the server revoked its credential")
	}
	if len(decodeLiveMachines(t, f.machineList(t)).Machines) != 0 {
		t.Error("revoked machine remains in the Machines list")
	}
	want := "✗ This computer was unpaired on the server.\n  Pair it again with: tervi pair --server " + f.serverURL + "\n"
	if code, output := f.status(t); code != 1 || output != want {
		t.Errorf("tervi status after revoke = (%d, %q), want exit 1 and unpaired message", code, output)
	}
	for _, command := range []string{"connect", "disconnect"} {
		run, commandCode := f.runCommand(t, command)
		wantCode := 1
		if command == "disconnect" {
			wantCode = 0
		}
		if commandCode != wantCode || run.stdout.String() != want {
			t.Errorf("tervi %s after revoke = (%d, %q), want exit %d and unpaired message", command, commandCode, run.stdout.String(), wantCode)
		}
	}
	assertDatabaseMachineAndCredential(t, f, paired.machineID, "revoked")
	assertRevokedCredentialRefused(t, f, paired.credential)
	assertNoSecrets(t, f, paired.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, paired.credential)
}

func TestRevokeOfflineEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)
	f.runner.stop(t)
	if status := revokeMachineFromPage(t, f, paired.machineID); status != http.StatusNoContent {
		t.Fatalf("Machines page Revoke returned HTTP %d, want 204", status)
	}
	if len(decodeLiveMachines(t, f.machineList(t)).Machines) != 0 {
		t.Error("revoked machine remains in the Machines list")
	}
	f.runner.startDaemon(t)
	waitFor(t, f.daemonLogs, unpairedDaemonLine(f.serverURL), connectionWait)
	waitForControlState(t, f.controlClient, "not_connecting", connectionWait)
	if strings.Contains(f.daemonLogs.String(), "… Reconnecting (try 2)") {
		t.Error("daemon retried after receiving credential_revoked on its first connection attempt")
	}
	if got := strings.Count(f.daemonLogs.String(), "✓ Connected to "+f.serverURL); got != 1 {
		t.Errorf("connected log lines after offline revoke = %d, want 1", got)
	}
	state := f.stateSnapshot(t)
	if state.Connect || state.StopReason == nil || *state.StopReason != "credential_revoked" {
		t.Errorf("state after daemon started with revoked credential = %+v, want credential_revoked", state)
	}
	assertDatabaseMachineAndCredential(t, f, paired.machineID, "revoked")
	assertNoSecrets(t, f, paired.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, paired.credential)
}

func TestPairSameComputerAgainEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	first := pairLiveMachine(t, f)
	createdAt := machineCreatedAt(t, f, first.machineID)

	unpairRun, unpairCode := f.runCommand(t, "unpair", "--yes")
	if unpairCode != 0 {
		t.Fatalf("first unpair exit = %d, output=%q", unpairCode, unpairRun.stdout.String())
	}
	second := assertRepairedMachine(t, f, first, createdAt)
	if second.credential == first.credential {
		t.Error("pairing after unpair reused the earlier credential")
	}

	if status := revokeMachineFromPage(t, f, second.machineID); status != http.StatusNoContent {
		t.Fatalf("Machines page Revoke returned HTTP %d, want 204", status)
	}
	waitFor(t, f.daemonLogs, unpairedDaemonLine(f.serverURL), connectionWait)
	waitForControlState(t, f.controlClient, "not_connecting", connectionWait)
	thirdRun := f.startPair(context.Background())
	third := completeLivePairObserved(t, f, thirdRun, func(answer approvalState) {
		assertEarlierMachineUnpaired(t, answer)
	})
	if third.machineID != first.machineID {
		t.Errorf("machine ID after pairing following Revoke = %q, want earlier machine %q", third.machineID, first.machineID)
	}
	assertExactlyOneOnlineMachine(t, f, first.machineID)
	if got := machineCreatedAt(t, f, first.machineID); !got.Equal(createdAt) {
		t.Errorf("machine created_at after pairing again = %s, want preserved %s", got, createdAt)
	}
	if third.credential == second.credential {
		t.Error("pairing after Revoke reused the earlier credential")
	}
	assertNoSecrets(t, f, first.credential, second.credential, third.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, third.credential, first.credential, second.credential)
}

func TestUnpairNoSecretsEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	first := pairLiveMachine(t, f)
	if _, output := f.status(t); output == "" {
		t.Fatal("status output was empty during secret audit")
	}
	f.machinesPage(t)
	f.machineList(t)
	assertNoSecrets(t, f, first.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, first.credential)

	unpairRun, code := f.runCommand(t, "unpair", "--yes")
	if code != 0 || !strings.Contains(unpairRun.stdout.String(), "✓ Unpaired. This computer is no longer paired with ") {
		t.Fatalf("unpair = (%d, %q), want success", code, unpairRun.stdout.String())
	}
	f.machineList(t)
	assertNoSecrets(t, f, first.credential)
	assertCredentialNotInLocalFiles(t, f, first.credential)

	secondRun := f.startPair(context.Background())
	second := completeLivePairObserved(t, f, secondRun, func(answer approvalState) {
		assertEarlierMachineUnpaired(t, answer)
	})
	f.machinesPage(t)
	f.machineList(t)
	if second.machineID != first.machineID {
		t.Errorf("machine ID after pairing again = %q, want %q", second.machineID, first.machineID)
	}
	assertNoSecrets(t, f, first.credential, second.credential)
	assertCredentialOnlyInPrivateFile(t, f.stateDir, f.serviceDir, second.credential, first.credential)
}

func assertMachinesPageShowsOffline(t *testing.T, f *connectedFixture, machineID string) {
	t.Helper()
	page := f.machinesPage(t)
	if !strings.Contains(page, "Machines") || !strings.Contains(page, "/static/machines.js") {
		t.Fatal("Machines page did not load its machine view")
	}
	machine := f.machine(t, machineID)
	if machine.Online || machine.LastSeenSecondsAgo == nil {
		t.Fatalf("Machines API after disconnect = %+v, want offline and last seen", machine)
	}
	asset, err := http.Get(f.serverURL + "/static/machines-view.js")
	if err != nil {
		t.Fatalf("GET Machines view script: %v", err)
	}
	data, err := io.ReadAll(asset.Body)
	_ = asset.Body.Close()
	if err != nil {
		t.Fatalf("read Machines view script: %v", err)
	}
	f.pageAnswers = append(f.pageAnswers, string(data))
	if asset.StatusCode != http.StatusOK || !strings.Contains(string(data), "Offline · ${lastSeen}") || !strings.Contains(string(data), "last seen ${timeAgo(machine.last_seen_seconds_ago)}") {
		t.Error("Machines page view does not render an offline machine with its last-seen time")
	}
}

func waitForOfflineIntervals(t *testing.T, f *connectedFixture, machineID string, intervals int, interval time.Duration) {
	t.Helper()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for i := 0; i < intervals; i++ {
		<-ticker.C
		if machine := f.machine(t, machineID); machine.Online {
			t.Fatalf("machine became Online during disconnected interval %d of %d", i+1, intervals)
		}
	}
}

func revokeMachineFromPage(t *testing.T, f *connectedFixture, machineID string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.serverURL+"/api/v1/machines/"+machineID+"/revoke", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST Machines page Revoke: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Machines page Revoke response: %v", err)
	}
	f.apiAnswers = append(f.apiAnswers, fmt.Sprintf("status=%d body=%s", response.StatusCode, body))
	return response.StatusCode
}

func assertRevokedCredentialRefused(t *testing.T, f *connectedFixture, machineCredential string) {
	t.Helper()
	url := f.serverURL + "/api/v1/machines/current/connection"
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+machineCredential)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request connection using revoked credential: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read revoked connection response: %v", err)
	}
	f.apiAnswers = append(f.apiAnswers, fmt.Sprintf("status=%d body=%s", response.StatusCode, body))
	var answer struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || response.StatusCode != http.StatusUnauthorized || answer.Error != "credential_revoked" {
		t.Errorf("connection with revoked credential = HTTP %d %s, want 401 credential_revoked", response.StatusCode, body)
	}
}

func assertDatabaseMachineAndCredential(t *testing.T, f *connectedFixture, machineID, status string) {
	t.Helper()
	var actual string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM credentials WHERE machine_id = $1 ORDER BY created_at DESC, credential_id DESC LIMIT 1`, machineID).Scan(&actual); err != nil {
		t.Fatalf("read machine credential status: %v", err)
	}
	if actual != status {
		t.Errorf("credential status = %q, want %q", actual, status)
	}
	var exists bool
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM machines WHERE machine_id = $1)`, machineID).Scan(&exists); err != nil {
		t.Fatalf("check machine row: %v", err)
	}
	if !exists {
		t.Error("unpair or Revoke deleted the machine history row")
	}
}

func unpairedDaemonLine(serverURL string) string {
	return "✗ This computer was unpaired on the server. Pair it again with: tervi pair --server " + serverURL
}

func hasBackgroundCall(calls []backgroundCall, name string, args string) bool {
	for _, call := range calls {
		if call.name == name && strings.Join(call.args, " ") == args {
			return true
		}
	}
	return false
}

func assertRepairedMachine(t *testing.T, f *connectedFixture, earlier livePair, createdAt time.Time) livePair {
	t.Helper()
	run := f.startPair(context.Background())
	newPair := completeLivePairObserved(t, f, run, func(answer approvalState) {
		assertEarlierMachineUnpaired(t, answer)
	})
	if newPair.machineID != earlier.machineID {
		t.Errorf("machine ID after pairing again = %q, want %q", newPair.machineID, earlier.machineID)
	}
	assertExactlyOneOnlineMachine(t, f, earlier.machineID)
	if got := machineCreatedAt(t, f, earlier.machineID); !got.Equal(createdAt) {
		t.Errorf("machine created_at after pairing again = %s, want preserved %s", got, createdAt)
	}
	return newPair
}

func assertEarlierMachineUnpaired(t *testing.T, answer approvalState) {
	t.Helper()
	if answer.EarlierMachine == nil {
		t.Fatal("approval answer omitted earlier_machine")
	}
	if answer.EarlierMachine.StillPaired == nil {
		t.Fatal("approval earlier_machine omitted still_paired")
	}
	if *answer.EarlierMachine.StillPaired {
		t.Error("approval earlier_machine says still_paired=true after unpair or Revoke")
	}
	if answer.EarlierMachine.UnpairedAt == nil {
		t.Error("approval earlier_machine omitted the earlier unpair time")
	}
}

func assertExactlyOneOnlineMachine(t *testing.T, f *connectedFixture, expectedID string) {
	t.Helper()
	machines := decodeLiveMachines(t, f.machineList(t)).Machines
	if len(machines) != 1 || machines[0].MachineID != expectedID || !machines[0].Online {
		t.Errorf("Machines list after pairing again = %+v, want the earlier machine as the only Online entry", machines)
	}
}

func machineCreatedAt(t *testing.T, f *connectedFixture, machineID string) time.Time {
	t.Helper()
	var createdAt time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT created_at FROM machines WHERE machine_id = $1`, machineID).Scan(&createdAt); err != nil {
		t.Fatalf("read machine creation time: %v", err)
	}
	return createdAt
}

func assertNoSecrets(t *testing.T, f *connectedFixture, machineCredentials ...string) {
	t.Helper()
	if len(machineCredentials) > 0 {
		assertNoConnectionSecret(t, f.audit(machineCredentials...))
	}
	localMachineID, err := os.ReadFile(f.machineIDPath)
	if err != nil {
		t.Fatalf("read fixture machine ID: %v", err)
	}
	var identity string
	if len(f.workerRuns) == 0 || f.lastMachineID == "" {
		t.Fatal("identity audit has no pair run")
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT encode(identity_hash, 'hex') FROM machines WHERE machine_id = $1`, f.lastMachineID).Scan(&identity); err != nil {
		t.Fatalf("read identity hash for audit: %v", err)
	}
	contents := f.audit(machineCredentials...).contents
	for name, value := range map[string]string{"fixture machine ID": strings.TrimSpace(string(localMachineID)), "identity hash": identity} {
		if value != "" && strings.Contains(contents, value) {
			t.Errorf("%s appeared in command output, logs, page, or API answers", name)
		}
	}
	for _, dir := range []string{f.stateDir, f.serviceDir} {
		if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
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
			for name, value := range map[string]string{"fixture machine ID": strings.TrimSpace(string(localMachineID)), "identity hash": identity} {
				if value != "" && strings.Contains(string(data), value) {
					t.Errorf("%s appeared in file %q", name, path)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("inspect secret audit folder %q: %v", dir, err)
		}
	}
}

func assertCredentialNotInLocalFiles(t *testing.T, f *connectedFixture, credentials ...string) {
	t.Helper()
	for _, dir := range []string{f.stateDir, f.serviceDir} {
		if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
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
			for _, value := range credentials {
				if strings.Contains(string(data), value) {
					t.Errorf("credential remained in file %q after unpair", path)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("inspect unpaired folder %q: %v", dir, err)
		}
	}
}
