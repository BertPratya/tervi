package daemon

import (
	"time"
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
	Path      string
}

// Status returns a snapshot of the daemon's current connection state.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := d.status
	if result.State == "reconnecting" && !result.NextTry.IsZero() {
		result.NextTryInSeconds = wholeSeconds(result.NextTry.Sub(d.opts.Now()))
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
