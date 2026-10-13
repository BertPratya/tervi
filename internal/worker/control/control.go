// Package control provides the daemon's private local control socket.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bertpratya/tervi/internal/worker/daemon"
)

var (
	// ErrAlreadyRunning means another daemon holds this user's runtime lock.
	ErrAlreadyRunning = errors.New("tervi daemon is already running")
	// ErrNotRunning means the daemon socket did not answer the request.
	ErrNotRunning = errors.New("tervi daemon is not running")
	// ErrRuntimeDirNotSet means XDG_RUNTIME_DIR is not available.
	ErrRuntimeDirNotSet = errors.New("XDG_RUNTIME_DIR is not set")
)

const (
	statusTimeout  = 5 * time.Second
	connectTimeout = 35 * time.Second
)

// Engine is the daemon interface exposed through the control socket.
type Engine interface {
	Run(context.Context) error
	Status() daemon.Status
	Connect(context.Context) daemon.ConnectResult
}

// StatusAnswer is the JSON representation returned by GET /status.
type StatusAnswer struct {
	State            string  `json:"state"`
	Server           string  `json:"server,omitempty"`
	Since            string  `json:"since,omitempty"`
	Try              int     `json:"try,omitempty"`
	NextTryInSeconds int     `json:"next_try_in_seconds,omitempty"`
	LastError        *string `json:"last_error,omitempty"`
	StopReason       *string `json:"stop_reason,omitempty"`
}

// ConnectAnswer is the JSON representation returned by POST /connect.
type ConnectAnswer struct {
	Result    string  `json:"result"`
	LastError *string `json:"last_error,omitempty"`
	Path      string  `json:"path,omitempty"`
}

// RuntimeDir returns the tervi directory below this session's runtime folder.
// The caller that starts the daemon creates it; clients can use the same path
// without creating runtime files when no daemon is running.
func RuntimeDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		return "", ErrRuntimeDirNotSet
	}
	return filepath.Join(base, "tervi"), nil
}

func statusAnswer(status daemon.Status) StatusAnswer {
	answer := StatusAnswer{State: status.State}
	switch status.State {
	case "connected":
		answer.Server = status.Server
		answer.Since = status.ConnectedSince.Format(time.RFC3339)
	case "reconnecting":
		answer.Server = status.Server
		answer.Try = status.Try
		answer.NextTryInSeconds = status.NextTryInSeconds
		answer.LastError = optionalText(status.LastError)
	case "not_connecting":
		answer.StopReason = status.StopReason
	}
	return answer
}

func (answer StatusAnswer) MarshalJSON() ([]byte, error) {
	switch answer.State {
	case "connected":
		return json.Marshal(struct {
			State  string `json:"state"`
			Server string `json:"server"`
			Since  string `json:"since"`
		}{answer.State, answer.Server, answer.Since})
	case "reconnecting":
		return json.Marshal(struct {
			State            string  `json:"state"`
			Server           string  `json:"server"`
			Try              int     `json:"try"`
			NextTryInSeconds int     `json:"next_try_in_seconds"`
			LastError        *string `json:"last_error"`
		}{answer.State, answer.Server, answer.Try, answer.NextTryInSeconds, answer.LastError})
	case "not_connecting":
		return json.Marshal(struct {
			State      string  `json:"state"`
			StopReason *string `json:"stop_reason"`
		}{answer.State, answer.StopReason})
	default:
		return json.Marshal(struct {
			State string `json:"state"`
		}{answer.State})
	}
}

func connectAnswer(result daemon.ConnectResult) ConnectAnswer {
	answer := ConnectAnswer{Result: result.Result}
	switch result.Result {
	case "still_trying":
		answer.LastError = optionalText(result.LastError)
	case "failed":
		answer.Path = result.Path
	}
	return answer
}

func (answer ConnectAnswer) MarshalJSON() ([]byte, error) {
	switch answer.Result {
	case "still_trying":
		return json.Marshal(struct {
			Result    string  `json:"result"`
			LastError *string `json:"last_error"`
		}{answer.Result, answer.LastError})
	case "failed":
		return json.Marshal(struct {
			Result string `json:"result"`
			Path   string `json:"path"`
		}{answer.Result, answer.Path})
	default:
		return json.Marshal(struct {
			Result string `json:"result"`
		}{answer.Result})
	}
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func ensureRuntimeDir(path string) error {
	if path == "" {
		return fmt.Errorf("runtime directory path is empty")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("secure runtime directory: %w", err)
	}
	return nil
}
