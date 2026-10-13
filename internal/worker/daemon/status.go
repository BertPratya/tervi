package daemon

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

// Status describes the daemon's connection state.
type Status struct {
	State            string
	Server           string
	ConnectedSince   time.Time
	Try              int
	NextTry          time.Time
	NextTryInSeconds int
	LastError        string
	StopReason       *string
}

// ConnectResult is the result of asking the daemon to connect.
type ConnectResult struct {
	Result    string
	LastError string
}

// Status returns a snapshot of the daemon's current connection state.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := d.status
	if result.State == "reconnecting" && !result.NextTry.IsZero() {
		seconds := math.Ceil(result.NextTry.Sub(d.opts.Now()).Seconds())
		if seconds > 0 {
			result.NextTryInSeconds = int(seconds)
		}
	}
	if result.StopReason != nil {
		reason := *result.StopReason
		result.StopReason = &reason
	}
	return result
}

func (d *Daemon) setStatusLocked(status Status) {
	d.status = status
	close(d.changed)
	d.changed = make(chan struct{})
}

func (d *Daemon) setTrying(try int, server string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setStatusLocked(Status{State: "reconnecting", Server: server, Try: try, NextTry: now, LastError: d.status.LastError})
}

func (d *Daemon) setRetry(try int, server, lastError string, next time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setStatusLocked(Status{State: "reconnecting", Server: server, Try: try, NextTry: next, LastError: lastError})
}

func (d *Daemon) setConnected(server string, since time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setStatusLocked(Status{State: "connected", Server: server, ConnectedSince: since})
}

func (d *Daemon) setRejected() {
	reason := "credential_rejected"
	d.mu.Lock()
	defer d.mu.Unlock()
	d.enabled = false
	d.setStatusLocked(Status{State: "not_connecting", StopReason: &reason})
}

func (d *Daemon) currentServer() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status.Server
}

func (d *Daemon) logLine(format string, args ...any) {
	_, _ = fmt.Fprintf(d.opts.Log, "%s %s\n", d.opts.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// Connect enables connection attempts and waits for a connection or rejection.
func (d *Daemon) Connect(ctx context.Context) ConnectResult {
	if ctx == nil {
		ctx = context.Background()
	}
	d.connectMu.Lock()
	defer d.connectMu.Unlock()

	entry, record, paired, err := credential.Paired(d.opts.StateDir, d.opts.Store)
	if err != nil && isStateReadError(err) {
		return ConnectResult{Result: "still_trying", LastError: "can't read state.json"}
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
		return ConnectResult{Result: "still_trying", LastError: "can't write state.json"}
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

func (d *Daemon) reject(record state.State, server string) {
	record.Connect = false
	reason := "credential_rejected"
	record.StopReason = &reason
	_ = state.WriteState(d.opts.StateDir, record)
	d.setRejected()
	d.logLine("✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server %s", server)
}

func isStateReadError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "read state.json:")
}
