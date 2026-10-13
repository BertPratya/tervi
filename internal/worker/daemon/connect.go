package daemon

import (
	"context"
	"time"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

// Connect enables connection attempts and waits for a connection or rejection.
func (d *Daemon) Connect(ctx context.Context) ConnectResult {
	if ctx == nil {
		ctx = context.Background()
	}
	d.connectMu.Lock()
	defer d.connectMu.Unlock()

	entry, record, paired, err := credential.Paired(d.opts.StateDir, d.opts.Store)
	if err != nil && isStateReadError(err) {
		lastError, _ := errorText(err, nil, "")
		return ConnectResult{Result: "still_trying", LastError: lastError}
	}
	if !record.Confirmed || !paired && err == nil {
		return ConnectResult{Result: "not_paired"}
	}
	if d.Status().State == "connected" {
		return ConnectResult{Result: "already_connected"}
	}
	record.Connect = true
	record.StopReason = nil
	if writeErr := state.WriteState(d.opts.StateDir, record); writeErr != nil {
		return ConnectResult{Result: "failed", Path: state.StatePath(d.opts.StateDir)}
	}
	d.mu.Lock()
	d.enabled = true
	d.setStatusLocked(Status{State: "reconnecting", Server: entry.Server, Try: 1, NextTry: d.opts.Now(), LastError: d.status.LastError})
	d.mu.Unlock()
	d.signal()
	return d.waitForConnect(ctx)
}

func (d *Daemon) waitForConnect(ctx context.Context) ConnectResult {
	timer := time.NewTimer(d.connectTimeout)
	defer timer.Stop()
	for {
		d.mu.Lock()
		status := d.status
		changed := d.changed
		if status.StopReason != nil {
			reason := *status.StopReason
			status.StopReason = &reason
		}
		d.mu.Unlock()
		if status.State == "connected" {
			return ConnectResult{Result: "connected"}
		}
		if status.State == "not_connecting" && status.StopReason != nil && *status.StopReason == "credential_rejected" {
			return ConnectResult{Result: "rejected", LastError: status.LastError}
		}
		select {
		case <-ctx.Done():
			return ConnectResult{Result: "still_trying", LastError: d.Status().LastError}
		case <-timer.C:
			return ConnectResult{Result: "still_trying", LastError: d.Status().LastError}
		case <-changed:
		}
	}
}

func (d *Daemon) reject(server string) {
	record, exists, err := state.ReadState(d.opts.StateDir)
	if err == nil && exists {
		record.Connect = false
		reason := "credential_rejected"
		record.StopReason = &reason
		_ = state.WriteState(d.opts.StateDir, record)
	}
	d.setRejected()
	d.logLine("✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server %s", server)
}
