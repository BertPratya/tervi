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

const statusTimeout = 5 * time.Second

var connectTimeout = 35 * time.Second
var disconnectTimeout = 40 * time.Second

// Engine is the daemon interface exposed through the control socket.
type Engine interface {
	Run(context.Context) error
	Status() daemon.Status
	Connect(context.Context) daemon.ConnectResult
	Disconnect(context.Context) daemon.DisconnectResult
}

// StatusAnswer is the JSON representation returned by GET /status.
type StatusAnswer struct {
	State            string
	Server           string
	Since            string
	Try              int
	NextTryInSeconds int
	LastError        *string
	StopReason       *string
}

// ConnectAnswer is the JSON representation returned by POST /connect.
type ConnectAnswer struct {
	Result    string
	LastError *string
	Path      string
}

// DisconnectAnswer is the JSON representation returned by POST /disconnect.
type DisconnectAnswer struct {
	Result string
	Path   string
}

// statusAnswerJSON is the single definition of status answer field names used
// by both encoding and decoding.
type statusAnswerJSON struct {
	State            string   `json:"state"`
	Server           *string  `json:"server,omitempty"`
	Since            *string  `json:"since,omitempty"`
	Try              *int     `json:"try,omitempty"`
	NextTryInSeconds *int     `json:"next_try_in_seconds,omitempty"`
	LastError        **string `json:"last_error,omitempty"`
	StopReason       **string `json:"stop_reason,omitempty"`
}

// connectAnswerJSON is the single definition of connect answer field names
// used by both encoding and decoding.
type connectAnswerJSON struct {
	Result    string   `json:"result"`
	LastError **string `json:"last_error,omitempty"`
	Path      *string  `json:"path,omitempty"`
}

type disconnectAnswerJSON struct {
	Result string  `json:"result"`
	Path   *string `json:"path,omitempty"`
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
	case daemon.StateConnected:
		answer.Server = status.Server
		answer.Since = status.ConnectedSince.Format(time.RFC3339)
	case daemon.StateReconnecting:
		answer.Server = status.Server
		answer.Try = status.Try
		answer.NextTryInSeconds = status.NextTryInSeconds
		answer.LastError = optionalText(status.LastError)
	case daemon.StateNotConnecting:
		answer.StopReason = status.StopReason
	}
	return answer
}

func (answer StatusAnswer) MarshalJSON() ([]byte, error) {
	wire := statusAnswerJSON{State: answer.State}
	switch answer.State {
	case daemon.StateConnected:
		wire.Server = &answer.Server
		wire.Since = &answer.Since
	case daemon.StateReconnecting:
		wire.Server = &answer.Server
		wire.Try = &answer.Try
		wire.NextTryInSeconds = &answer.NextTryInSeconds
		wire.LastError = nullableString(answer.LastError)
	case daemon.StateNotConnecting:
		wire.StopReason = nullableString(answer.StopReason)
	}
	return json.Marshal(wire)
}

func (answer *StatusAnswer) UnmarshalJSON(data []byte) error {
	var wire statusAnswerJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*answer = StatusAnswer{State: wire.State}
	if wire.Server != nil {
		answer.Server = *wire.Server
	}
	if wire.Since != nil {
		answer.Since = *wire.Since
	}
	if wire.Try != nil {
		answer.Try = *wire.Try
	}
	if wire.NextTryInSeconds != nil {
		answer.NextTryInSeconds = *wire.NextTryInSeconds
	}
	if wire.LastError != nil {
		answer.LastError = *wire.LastError
	}
	if wire.StopReason != nil {
		answer.StopReason = *wire.StopReason
	}
	return nil
}

func connectAnswer(result daemon.ConnectResult) ConnectAnswer {
	answer := ConnectAnswer{Result: result.Result}
	switch result.Result {
	case daemon.ResultStillTrying:
		answer.LastError = optionalText(result.LastError)
	case daemon.ResultFailed:
		answer.Path = result.Path
	}
	return answer
}

func (answer ConnectAnswer) MarshalJSON() ([]byte, error) {
	wire := connectAnswerJSON{Result: answer.Result}
	switch answer.Result {
	case daemon.ResultStillTrying:
		wire.LastError = nullableString(answer.LastError)
	case daemon.ResultFailed:
		wire.Path = &answer.Path
	}
	return json.Marshal(wire)
}

func (answer *ConnectAnswer) UnmarshalJSON(data []byte) error {
	var wire connectAnswerJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*answer = ConnectAnswer{Result: wire.Result}
	if wire.LastError != nil {
		answer.LastError = *wire.LastError
	}
	if wire.Path != nil {
		answer.Path = *wire.Path
	}
	return nil
}

func disconnectAnswer(result daemon.DisconnectResult) DisconnectAnswer {
	answer := DisconnectAnswer{Result: result.Result}
	if result.Result == daemon.ResultFailed || result.Result == daemon.ResultUnreadable {
		answer.Path = result.Path
	}
	return answer
}

func (answer DisconnectAnswer) MarshalJSON() ([]byte, error) {
	wire := disconnectAnswerJSON{Result: answer.Result}
	if answer.Result == daemon.ResultFailed || answer.Result == daemon.ResultUnreadable {
		wire.Path = &answer.Path
	}
	return json.Marshal(wire)
}

func (answer *DisconnectAnswer) UnmarshalJSON(data []byte) error {
	var wire disconnectAnswerJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*answer = DisconnectAnswer{Result: wire.Result}
	if wire.Path != nil {
		answer.Path = *wire.Path
	}
	return nil
}

func nullableString(value *string) **string { return &value }

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
