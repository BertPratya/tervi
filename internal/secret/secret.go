// Package secret provides values that are hidden by default when printed.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"unicode"
)

// Value holds a secret string behind a pointer to keep default struct printing safe.
type Value struct {
	p *string
}

// FromString wraps a string received from outside the process.
func FromString(value string) Value {
	return Value{p: &value}
}

// Reveal returns the secret value.
func (value Value) Reveal() string {
	if value.p == nil {
		return ""
	}
	return *value.p
}

// Format hides the value for every fmt formatting verb.
func (Value) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[hidden]"))
}

// LogValue hides the value in structured logs.
func (Value) LogValue() slog.Value {
	return slog.StringValue("[hidden]")
}

// MarshalJSON hides the value when encoded as JSON.
func (Value) MarshalJSON() ([]byte, error) {
	return json.Marshal("[hidden]")
}

// NewKey creates a cryptographically random key or credential.
func NewKey() (Value, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return Value{}, fmt.Errorf("generate secret key: %w", err)
	}
	return FromString(base64.RawURLEncoding.EncodeToString(bytes[:])), nil
}

// NewPairingCode creates an eight digit cryptographically random pairing code.
func NewPairingCode() (Value, error) {
	var code [9]byte
	for i := range code {
		if i == 4 {
			code[i] = '-'
			continue
		}
		digit, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return Value{}, fmt.Errorf("generate pairing code: %w", err)
		}
		code[i] = byte('0' + digit.Int64())
	}
	return FromString(string(code[:])), nil
}

// NormalizeCode removes hyphens and whitespace from a pairing code.
func NormalizeCode(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '-' || unicode.IsSpace(character) {
			return -1
		}
		return character
	}, value)
}

// Hash returns the SHA-256 digest of a secret value.
func Hash(value Value) []byte {
	digest := sha256.Sum256([]byte(value.Reveal()))
	return digest[:]
}
