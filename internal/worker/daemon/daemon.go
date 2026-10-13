// Package daemon maintains the worker's connection to its paired server.
package daemon

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

const connectWaitLimit = 30 * time.Second

type waitResult uint8

const (
	waitCancelled waitResult = iota
	waitElapsed
	waitWoken
)

// Options configures the daemon and its connection dependencies.
type Options struct {
	StateDir string
	Store    *credential.Store
	Log      io.Writer
	Now      func() time.Time
	Rand     func() float64
	Dial     func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

// Daemon runs the worker connection loop and exposes its current state.
type Daemon struct {
	opts Options

	mu      sync.Mutex
	status  Status
	changed chan struct{}
	wake    chan struct{}
	enabled bool

	connectTimeout time.Duration
	waitOverride   func(time.Duration, context.Context) bool

	connectMu sync.Mutex
}

// New creates a daemon. Run owns its connection loop; Connect wakes it.
func New(opts Options) *Daemon {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Rand == nil {
		opts.Rand = rand.Float64
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Dial == nil {
		opts.Dial = websocket.Dial
	}
	d := &Daemon{
		opts:           opts,
		changed:        make(chan struct{}),
		wake:           make(chan struct{}, 1),
		connectTimeout: connectWaitLimit,
	}
	d.status.State = "not_connecting"
	return d
}

// Run follows state.json until ctx ends. It returns nil when stopped.
func (d *Daemon) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.refreshState()
	try := 1
	announce := false
	for ctx.Err() == nil {
		if !d.isEnabled() {
			select {
			case <-ctx.Done():
				return nil
			case <-d.wake:
				d.refreshState()
				try, announce = 1, false
			}
			continue
		}

		if announce {
			d.logLine("… Reconnecting (try %d)", try)
		}
		d.setTrying(try, d.currentServer(), d.opts.Now())
		result := d.tryConnection(ctx, try)
		if ctx.Err() != nil {
			return nil
		}
		switch result.kind {
		case attemptIdle:
			d.refreshState()
			try, announce = 1, false
		case attemptRejected:
			d.reject(result.record, result.server)
			try, announce = 1, false
		case attemptFailed:
			wait := reconnectWait(try+1, d.opts.Rand())
			d.logLine("✗ %s. Next try in %d seconds", result.logError, int(wait.Seconds()+0.5))
			d.setRetry(try+1, result.server, result.lastError, d.opts.Now().Add(wait))
			waited := d.wait(wait, ctx)
			if waited == waitCancelled {
				return nil
			}
			if waited == waitWoken || d.refreshOnWake() {
				try, announce = 1, false
			} else {
				try, announce = try+1, true
			}
		case attemptConnected:
			d.setConnected(result.server, d.opts.Now())
			d.drainWake()
			d.logLine("✓ Connected to %s", result.server)
			lost := d.serveConnection(ctx, result.conn, result.hello)
			if ctx.Err() != nil {
				if !lost.closeStarted {
					_ = result.conn.Close(websocket.StatusCode(1000), "daemon stopped")
				}
				return nil
			}
			if !lost.closeStarted {
				_ = result.conn.CloseNow()
			}
			d.logLine("✗ Connection lost: %s", lost.reason)
			wait := reconnectWait(1, d.opts.Rand())
			d.setRetry(1, result.server, lost.lastError, d.opts.Now().Add(wait))
			waited := d.wait(wait, ctx)
			if waited == waitCancelled {
				return nil
			}
			if waited == waitWoken || d.refreshOnWake() {
				try, announce = 1, false
			} else {
				try, announce = 1, true
			}
		}
	}
	return nil
}

func (d *Daemon) refreshOnWake() bool {
	select {
	case <-d.wake:
		d.refreshState()
		return true
	default:
		return false
	}
}

func (d *Daemon) drainWake() {
	select {
	case <-d.wake:
	default:
	}
}

func (d *Daemon) wait(duration time.Duration, ctx context.Context) waitResult {
	if d.waitOverride != nil {
		if d.waitOverride(duration, ctx) {
			return waitElapsed
		}
		return waitCancelled
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return waitCancelled
	case <-d.wake:
		d.refreshState()
		return waitWoken
	case <-timer.C:
		return waitElapsed
	}
}

func (d *Daemon) refreshState() {
	record, exists, err := state.ReadState(d.opts.StateDir)
	if err != nil {
		d.mu.Lock()
		d.enabled = false
		d.setStatusLocked(Status{State: "not_connecting"})
		d.mu.Unlock()
		return
	}
	if !exists || !record.Confirmed {
		d.mu.Lock()
		d.enabled = false
		d.setStatusLocked(Status{State: "not_paired"})
		d.mu.Unlock()
		return
	}
	if !record.Connect {
		d.mu.Lock()
		d.enabled = false
		d.setStatusLocked(Status{State: "not_connecting", StopReason: record.StopReason})
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	lastError := d.status.LastError
	d.enabled = true
	d.setStatusLocked(Status{State: "reconnecting", Try: 1, NextTry: d.opts.Now(), LastError: lastError})
	d.mu.Unlock()
}

func (d *Daemon) isEnabled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.enabled
}

func (d *Daemon) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
