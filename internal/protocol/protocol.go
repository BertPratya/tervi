// Package protocol defines the connection messages, close codes, and credential
// error codes shared by the server and the worker.
package protocol

import (
	"bytes"
	"encoding/json"
	"time"
)

const (
	TypeHello = "hello"
	TypePing  = "ping"
	TypePong  = "pong"

	MaxMessageSize = 4096

	CloseNormal         = 1000
	CloseServerStopping = 1001
	CloseInvalidMessage = 1008
	CloseTooLarge       = 1009
	CloseReplaced       = 4000
	CloseTimeout        = 4001
	CloseRevoked        = 4002

	ErrorUnknownCredential = "unknown_credential"
	ErrorCredentialRevoked = "credential_revoked"
)

const DefaultHelloTimeout = 30 * time.Second

// Hello is the server's first connection message.
type Hello struct {
	Type                 string `json:"type"`
	CheckIntervalSeconds int    `json:"check_interval_seconds"`
	TimeoutSeconds       int    `json:"timeout_seconds"`
}

// Ping is the worker's application-level keepalive message.
type Ping struct {
	Type string `json:"type"`
}

// Pong is the server's answer to a worker ping.
type Pong struct {
	Type string `json:"type"`
}

// ReadType extracts a string type from a JSON object. The boolean is false
// when the message is not an object with a string type field.
func ReadType(data []byte) (string, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return "", false
	}
	rawType, exists := object["type"]
	trimmedType := bytes.TrimSpace(rawType)
	if !exists || len(trimmedType) < 2 || trimmedType[0] != '"' {
		return "", false
	}
	var messageType string
	if err := json.Unmarshal(trimmedType, &messageType); err != nil {
		return "", false
	}
	return messageType, true
}

// PingMessage returns the encoded application keepalive sent by the worker.
func PingMessage() []byte { return marshalMessage(Ping{Type: TypePing}) }

// PongMessage returns the encoded answer sent by the server to a ping.
func PongMessage() []byte { return marshalMessage(Pong{Type: TypePong}) }

func marshalMessage(message any) []byte {
	data, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	return data
}
