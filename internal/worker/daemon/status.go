package daemon

import (
	"time"
)

// State and result names are shared with the control socket and CLI.
const (
	StateConnected     = "connected"
	StateReconnecting  = "reconnecting"
	StateNotConnecting = "not_connecting"
	StateNotPaired     = "not_paired"

	ResultConnected           = "connected"
	ResultAlreadyConnected    = "already_connected"
	ResultRejected            = "rejected"
	ResultRevoked             = "revoked"
	ResultNotPaired           = "not_paired"
	ResultStillTrying         = "still_trying"
	ResultFailed              = "failed"
	ResultDisconnected        = "disconnected"
	ResultAlreadyDisconnected = "already_disconnected"
	ResultUnreadable          = "unreadable"
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

// DisconnectResult is the outcome of asking the daemon to stop connecting.
type DisconnectResult struct {
	Result string
	Path   string
}

// Status returns a snapshot of the daemon's current connection state.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := d.status
	if result.State == StateReconnecting && !result.NextTry.IsZero() {
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

func (d *Daemon) knownServerLocked(server string) string {
	if server != "" {
		return server
	}
	return d.status.Server
}

func (d *Daemon) setTrying(try int, server string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	server = d.knownServerLocked(server)
	d.setStatusLocked(Status{State: StateReconnecting, Server: server, Try: try, NextTry: now, LastError: d.status.LastError})
}

func (d *Daemon) currentServer() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status.Server
}
