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
)

// Client sends requests to the daemon in a user's runtime directory.
type Client struct {
	RuntimeDir string
}

// Status asks the running daemon for its current connection state.
func (client Client) Status(ctx context.Context) (StatusAnswer, error) {
	var answer StatusAnswer
	_, _, err := request(ctx, client.RuntimeDir, http.MethodGet, "/status", statusTimeout, &answer)
	if err != nil {
		return StatusAnswer{}, err
	}
	return answer, nil
}

// Connect asks the daemon to enable its connection and waits for the result.
func (client Client) Connect(ctx context.Context) (ConnectAnswer, error) {
	var answer ConnectAnswer
	reachedSocket, timedOut, err := request(ctx, client.RuntimeDir, http.MethodPost, "/connect", connectTimeout, &answer)
	if err != nil {
		if reachedSocket && timedOut {
			return ConnectAnswer{Result: "still_trying"}, nil
		}
		return ConnectAnswer{}, err
	}
	return answer, nil
}

func request(ctx context.Context, runtimeDir, method, path string, timeout time.Duration, answer any) (bool, bool, error) {
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
		return reachedSocket.Load(), false, err
	}
	response, err := client.Do(req)
	if err != nil {
		return requestFailure(ctx, requestCtx, reachedSocket.Load(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return reachedSocket.Load(), false, fmt.Errorf("daemon answered HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(answer); err != nil {
		if ctx.Err() != nil {
			return reachedSocket.Load(), false, ctx.Err()
		}
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return requestFailure(ctx, requestCtx, reachedSocket.Load(), fmt.Errorf("decode daemon response: %w", err))
		}
		return reachedSocket.Load(), false, fmt.Errorf("decode daemon response: %w", err)
	}
	return reachedSocket.Load(), false, nil
}

func requestFailure(ctx, requestCtx context.Context, reachedSocket bool, err error) (bool, bool, error) {
	if ctx.Err() != nil {
		return reachedSocket, false, ctx.Err()
	}
	if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
		return reachedSocket, true, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	return reachedSocket, false, fmt.Errorf("%w: %v", ErrNotRunning, err)
}
