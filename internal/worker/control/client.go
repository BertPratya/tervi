package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/bertpratya/tervi/internal/worker/daemon"
)

// Client sends requests to the daemon in a user's runtime directory.
type Client struct {
	RuntimeDir string
}

type requestOutcome struct {
	socketReached    bool
	deadlineExceeded bool
	err              error
}

// Status asks the running daemon for its current connection state.
func (client Client) Status(ctx context.Context) (StatusAnswer, error) {
	var answer StatusAnswer
	outcome := request(ctx, client.RuntimeDir, http.MethodGet, "/status", statusTimeout, &answer)
	if outcome.err != nil {
		return StatusAnswer{}, outcome.err
	}
	return answer, nil
}

// Connect asks the daemon to enable its connection and waits for the result.
func (client Client) Connect(ctx context.Context) (ConnectAnswer, error) {
	var answer ConnectAnswer
	outcome := request(ctx, client.RuntimeDir, http.MethodPost, "/connect", connectTimeout, &answer)
	if outcome.err != nil {
		if outcome.socketReached && outcome.deadlineExceeded {
			return ConnectAnswer{Result: daemon.ResultStillTrying}, nil
		}
		return ConnectAnswer{}, outcome.err
	}
	return answer, nil
}

// Disconnect asks the daemon to stop connecting and waits for the close to
// reach the server.
func (client Client) Disconnect(ctx context.Context) (DisconnectAnswer, error) {
	var answer DisconnectAnswer
	outcome := request(ctx, client.RuntimeDir, http.MethodPost, "/disconnect", disconnectTimeout, &answer)
	if outcome.err != nil {
		return DisconnectAnswer{}, outcome.err
	}
	return answer, nil
}

func request(ctx context.Context, runtimeDir, method, path string, timeout time.Duration, answer any) requestOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reachedSocket atomic.Bool
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtimeDir, "daemon.sock"))
		if err == nil {
			reachedSocket.Store(true)
		}
		return conn, err
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(requestCtx, method, "http://tervi.local"+path, nil)
	if err != nil {
		return requestOutcome{socketReached: reachedSocket.Load(), err: err}
	}
	response, err := client.Do(req)
	if err != nil {
		return requestFailure(ctx, requestCtx, reachedSocket.Load(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusServiceUnavailable {
			return requestOutcome{
				socketReached: reachedSocket.Load(),
				err:           fmt.Errorf("%w: daemon is stopping", ErrNotRunning),
			}
		}
		return requestOutcome{
			socketReached: reachedSocket.Load(),
			err:           fmt.Errorf("daemon answered HTTP %d", response.StatusCode),
		}
	}
	if err := json.NewDecoder(response.Body).Decode(answer); err != nil {
		if ctx.Err() != nil {
			return requestOutcome{socketReached: reachedSocket.Load(), err: ctx.Err()}
		}
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return requestFailure(ctx, requestCtx, reachedSocket.Load(), fmt.Errorf("decode daemon response: %w", err))
		}
		return requestOutcome{
			socketReached: reachedSocket.Load(),
			err:           fmt.Errorf("decode daemon response: %w", err),
		}
	}
	return requestOutcome{socketReached: reachedSocket.Load()}
}

func requestFailure(ctx, requestCtx context.Context, reachedSocket bool, err error) requestOutcome {
	if ctx.Err() != nil {
		return requestOutcome{socketReached: reachedSocket, err: ctx.Err()}
	}
	if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
		return requestOutcome{
			socketReached:    reachedSocket,
			deadlineExceeded: true,
			err:              fmt.Errorf("%w: %v", ErrNotRunning, err),
		}
	}
	return requestOutcome{
		socketReached: reachedSocket,
		err:           fmt.Errorf("%w: %v", ErrNotRunning, err),
	}
}
