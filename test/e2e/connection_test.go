package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/coder/websocket"
)

func TestPairConnectsEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)

	if code, output := f.status(t); code != 0 || !strings.Contains(output, "● Connected to "+f.serverURL) {
		t.Errorf("tervi status exit = %d, contains connected status = %t; want exit 0 and connected", code, strings.Contains(output, "● Connected to "+f.serverURL))
	}
	if page := f.machinesPage(t); !strings.Contains(page, "Machines") {
		t.Errorf("Machines page omitted its heading")
	}
	f.assertStartCall(t)
	if !strings.Contains(f.daemonLogs.String(), "✓ Connected to "+f.serverURL) {
		t.Error("daemon log omitted the connected message")
	}
	if paired.machineID == "" {
		t.Fatal("pairing returned an empty machine ID")
	}
}

func TestServerRestartEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)
	f.stopServer()
	waitForLog(t, f.daemonLogs, "✗ Connection lost: the server is stopping", time.Second)
	waitForControlState(t, f.controlClient, "reconnecting", connectionWait)
	if code, output := f.status(t); code != 1 || !strings.Contains(output, "○ Reconnecting to "+f.serverURL) {
		t.Errorf("tervi status while stopped exit = %d, contains reconnecting status = %t", code, strings.Contains(output, "○ Reconnecting to "+f.serverURL))
	}

	f.startServer(nil)
	offline := f.machine(t, paired.machineID)
	if offline.Online {
		t.Fatal("machine was Online immediately after the server restarted; it should wait for the daemon to reconnect")
	}
	if offline.LastSeenSecondsAgo == nil {
		t.Error("offline machine omitted last_seen_seconds_ago after it had connected")
	}
	online := waitForMachineOnline(t, f, paired.machineID, true, connectionWait)
	if online.LastSeenSecondsAgo == nil {
		t.Error("reconnected machine omitted last_seen_seconds_ago")
	}
	waitForLogCount(t, f.daemonLogs, "✓ Connected to "+f.serverURL, 2, connectionWait)
	if code, output := f.status(t); code != 0 || !strings.Contains(output, "● Connected to "+f.serverURL) {
		t.Errorf("tervi status after restart exit = %d, contains connected status = %t", code, strings.Contains(output, "● Connected to "+f.serverURL))
	}
}

func TestNewestWinsEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	paired := pairLiveMachine(t, f)
	testConnection := dialMachineConnection(t, f.serverURL, paired.credential)
	defer testConnection.CloseNow()

	waitForLog(t, f.daemonLogs, "✗ Connection lost: replaced by a newer connection", connectionWait)
	if machine := f.machine(t, paired.machineID); !machine.Online {
		t.Fatal("machine went Offline when its first connection was replaced")
	}
	waitForLogCount(t, f.daemonLogs, "✓ Connected to "+f.serverURL, 2, connectionWait)
	waitForLogCount(t, f.serverLogs, "machine connection replaced", 2, connectionWait)
	if machine := f.machine(t, paired.machineID); !machine.Online {
		t.Fatal("machine went Offline when the daemon replaced the test connection")
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionWait)
	defer cancel()
	_, _, err := testConnection.Read(ctx)
	if err == nil {
		t.Fatal("replaced test connection remained open")
	}
	if got := websocket.CloseStatus(err); got != protocol.CloseReplaced {
		t.Errorf("test connection close code = %d, want %d", got, protocol.CloseReplaced)
	}
	logLines := strings.Split(f.serverLogs.String(), "\n")
	warnings := 0
	for _, line := range logLines {
		if strings.Contains(line, "machine connection replaced") && strings.Contains(line, paired.machineID) {
			warnings++
		}
	}
	if warnings != 2 {
		t.Errorf("server logged %d replacement warnings naming machine %q, want 2", warnings, paired.machineID)
	}
}

func TestRejectionAndPairAgainEndToEnd(t *testing.T) {
	f := newConnectedFixture(t)
	_, _ = rejectAndPairAgain(t, f)
}

func TestNoSecretsEndToEnd(t *testing.T) {
	t.Run("pair and connect", func(t *testing.T) {
		f := newConnectedFixture(t)
		paired := pairLiveMachine(t, f)
		f.status(t)
		f.machinesPage(t)
		assertNoConnectionSecret(t, f.audit(paired.credential))
	})
	t.Run("rejection and pair again", func(t *testing.T) {
		f := newConnectedFixture(t)
		oldPair, newPair := rejectAndPairAgain(t, f)
		assertNoConnectionSecret(t, f.audit(oldPair.credential, newPair.credential))
	})
}

func rejectAndPairAgain(t *testing.T, f *connectedFixture) (livePair, livePair) {
	t.Helper()
	oldPair := pairLiveMachine(t, f)
	if tag, err := f.pool.Exec(context.Background(), `UPDATE machines SET status = 'failed' WHERE machine_id = $1`, oldPair.machineID); err != nil {
		t.Fatalf("reject paired machine: %v", err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("rejected %d machine rows, want 1", tag.RowsAffected())
	}
	f.stopServer()
	waitForLog(t, f.daemonLogs, "✗ Connection lost: the server is stopping", connectionWait)
	f.startServer(nil)
	waitForCredentialRejected(t, f.controlClient, connectionWait)
	state := f.stateSnapshot(t)
	if state.Connect || state.StopReason == nil || *state.StopReason != "credential_rejected" {
		t.Errorf("state after rejection = %+v, want connect false and credential_rejected", state)
	}
	if code, output := f.status(t); code != 1 ||
		!strings.Contains(output, "✗ Not connecting: the server doesn't accept this computer's credential.") ||
		!strings.Contains(output, "Pair again with: tervi pair --server "+f.serverURL) {
		t.Errorf("tervi status after credential rejection exit = %d, rejection status = %t, re-pair instructions = %t", code,
			strings.Contains(output, "✗ Not connecting: the server doesn't accept this computer's credential."),
			strings.Contains(output, "Pair again with: tervi pair --server "+f.serverURL))
	}
	if !strings.Contains(f.daemonLogs.String(), "✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server "+f.serverURL) {
		t.Error("daemon log omitted credential rejection and re-pair instructions")
	}

	run := f.startPair(context.Background())
	waitFor(t, run.stdout, "The server no longer accepts this computer's credential. Starting a new pairing.")
	newPair := completeLivePair(t, f, run)
	if !strings.HasPrefix(run.stdout.String(), "The server no longer accepts this computer's credential. Starting a new pairing.\n") {
		t.Error("re-pair output omitted the R33 message")
	}
	if oldPair.credential == newPair.credential {
		t.Error("re-pair reused the rejected credential")
	}
	f.machinesPage(t)
	return oldPair, newPair
}

func waitForCredentialRejected(t *testing.T, client control.Client, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		answer, err := client.Status(context.Background())
		if err == nil && answer.State == "not_connecting" && answer.StopReason != nil && *answer.StopReason == "credential_rejected" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	answer, err := client.Status(context.Background())
	t.Fatalf("daemon status after rejection = (%q, %v), want credential_rejected", answer.State, err)
}
