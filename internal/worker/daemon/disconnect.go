package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

var errDaemonDisconnected = errors.New("daemon disconnected")

// Disconnect stops connecting: it writes connect: false, closes an open
// connection with 1000, and stops any wait for a reconnect.
func (d *Daemon) Disconnect(_ context.Context) DisconnectResult {
	d.connectMu.Lock()
	defer d.connectMu.Unlock()

	d.mu.Lock()
	result := DisconnectState(d.opts.StateDir, d.opts.Store)
	if result.Result != ResultDisconnected {
		d.mu.Unlock()
		return result
	}
	d.disconnectSeq++
	d.enabled = false
	d.setStatusLocked(Status{State: StateNotConnecting})
	cancelAttempt := d.attemptCancel
	conn := d.activeConn
	d.activeConn = nil
	d.logLine("○ Disconnected.")
	d.mu.Unlock()
	if cancelAttempt != nil {
		cancelAttempt(errDaemonDisconnected)
	}
	d.signal()
	if conn != nil {
		_ = conn.Close(websocket.StatusCode(protocol.CloseNormal), "daemon disconnected")
	}
	return result
}

// DisconnectState classifies the current pairing and connection state, and
// writes connect: false when a paired computer is connecting.
func DisconnectState(stateDir string, store *credential.Store) DisconnectResult {
	_, record, paired, err := credential.Paired(stateDir, store)
	if err != nil {
		path := credential.Path(stateDir)
		if errors.Is(err, credential.ErrStateUnreadable) {
			path = state.StatePath(stateDir)
		}
		return DisconnectResult{Result: ResultUnreadable, Path: path}
	}
	if !paired || !record.Confirmed {
		return DisconnectResult{Result: ResultNotPaired}
	}
	if record.StopReason != nil {
		switch *record.StopReason {
		case state.StopReasonCredentialRejected:
			return DisconnectResult{Result: ResultRejected}
		case state.StopReasonCredentialRevoked:
			return DisconnectResult{Result: ResultRevoked}
		}
	}
	if !record.Connect {
		return DisconnectResult{Result: ResultAlreadyDisconnected}
	}

	record.Connect = false
	record.StopReason = nil
	if err := state.WriteState(stateDir, record); err != nil {
		return DisconnectResult{Result: ResultFailed, Path: state.StatePath(stateDir)}
	}
	return DisconnectResult{Result: ResultDisconnected}
}

func (d *Daemon) startAttempt(parent context.Context, try int, server string, now time.Time, announce bool) (context.Context, uint64, context.CancelCauseFunc, bool) {
	attemptCtx, cancel := context.WithCancelCause(parent)
	d.mu.Lock()
	if !d.enabled || parent.Err() != nil {
		d.mu.Unlock()
		cancel(context.Canceled)
		return nil, 0, nil, false
	}
	if announce {
		d.logLine("… Reconnecting (try %d)", try)
	}
	server = d.knownServerLocked(server)
	d.setStatusLocked(Status{State: StateReconnecting, Server: server, Try: try, NextTry: now, LastError: d.status.LastError})
	d.attemptCancel = cancel
	sequence := d.disconnectSeq
	d.mu.Unlock()
	return attemptCtx, sequence, cancel, true
}

func (d *Daemon) finishAttempt(cancel context.CancelCauseFunc) {
	cancel(context.Canceled)
	d.mu.Lock()
	d.attemptCancel = nil
	d.mu.Unlock()
}

func (d *Daemon) wasDisconnected(sequence uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.disconnectSeq != sequence
}

func (d *Daemon) setServerForSequence(sequence uint64, server string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.disconnectSeq != sequence || !d.enabled {
		return false
	}
	status := d.status
	status.Server = d.knownServerLocked(server)
	d.setStatusLocked(status)
	return true
}

func (d *Daemon) finishAttemptIfDisconnected(sequence uint64, result attemptResult) bool {
	if !d.wasDisconnected(sequence) {
		return false
	}
	d.discardDisconnectedAttempt(result)
	d.refreshState()
	return true
}

func (d *Daemon) stopForSequenceOrRefresh(sequence uint64, server, reason string) {
	if !d.stopForSequence(sequence, server, reason) {
		d.refreshState()
	}
}

func (d *Daemon) startServing(sequence uint64, server string, conn *websocket.Conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.enabled || d.disconnectSeq != sequence {
		return false
	}
	d.activeConn = conn
	d.setStatusLocked(Status{State: StateConnected, Server: server, ConnectedSince: d.opts.Now()})
	d.logLine("✓ Connected to %s", server)
	return true
}

func (d *Daemon) startServingOrClose(sequence uint64, result attemptResult) bool {
	if d.startServing(sequence, result.server, result.conn) {
		return true
	}
	d.discardDisconnectedAttempt(result)
	return false
}

func (d *Daemon) disconnectedAfterServing(sequence uint64) bool {
	return d.wasDisconnected(sequence)
}

func (d *Daemon) finishServing(conn *websocket.Conn) {
	d.mu.Lock()
	if d.activeConn == conn {
		d.activeConn = nil
	}
	d.mu.Unlock()
}

func (d *Daemon) discardDisconnectedAttempt(result attemptResult) {
	if result.conn != nil {
		_ = result.conn.Close(websocket.StatusCode(protocol.CloseNormal), "daemon disconnected")
	}
}

func (d *Daemon) reportConnectionLost(sequence uint64, server, reason string, next time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.disconnectSeq != sequence {
		return false
	}
	d.logLine("✗ Connection lost: %s", reason)
	server = d.knownServerLocked(server)
	d.setStatusLocked(Status{State: StateReconnecting, Server: server, Try: 1, NextTry: next})
	return true
}

func (d *Daemon) reportAttemptFailed(sequence uint64, try int, result attemptResult, wait time.Duration) bool {
	d.mu.Lock()
	if d.disconnectSeq != sequence {
		d.mu.Unlock()
		d.refreshState()
		return false
	}
	d.logLine("✗ %s. Next try in %s", result.logError, SecondsText(wholeSeconds(wait)))
	server := d.knownServerLocked(result.server)
	d.setStatusLocked(Status{
		State: StateReconnecting, Server: server, Try: try + 1,
		NextTry: d.opts.Now().Add(wait), LastError: result.lastError,
	})
	d.mu.Unlock()
	return true
}

func (d *Daemon) stopForSequence(sequence uint64, server, reason string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.disconnectSeq != sequence {
		return false
	}
	if record, exists, err := state.ReadState(d.opts.StateDir); err == nil && exists {
		record.Connect = false
		record.StopReason = &reason
		_ = state.WriteState(d.opts.StateDir, record)
	}
	d.enabled = false
	d.setStatusLocked(Status{State: StateNotConnecting, StopReason: &reason})
	switch reason {
	case state.StopReasonCredentialRejected:
		d.logLine("✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server %s", server)
	case state.StopReasonCredentialRevoked:
		d.logLine("✗ This computer was unpaired on the server. Pair it again with: tervi pair --server %s", server)
	}
	return true
}
