package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// Status asks the running daemon for its current connection state.
func Status(ctx context.Context, runtimeDir string) (StatusAnswer, error) {
	var answer StatusAnswer
	if err := request(ctx, runtimeDir, http.MethodGet, "/status", statusTimeout, &answer); err != nil {
		return StatusAnswer{}, err
	}
	return answer, nil
}

// Connect asks the daemon to enable its connection and waits for the result.
func Connect(ctx context.Context, runtimeDir string) (ConnectAnswer, error) {
	var answer ConnectAnswer
	if err := request(ctx, runtimeDir, http.MethodPost, "/connect", connectTimeout, &answer); err != nil {
		return ConnectAnswer{}, err
	}
	return answer, nil
}

func request(ctx context.Context, runtimeDir, method, path string, timeout time.Duration, answer any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtimeDir, "daemon.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(requestCtx, method, "http://tervi.local"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("daemon answered HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(answer); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	return nil
}
