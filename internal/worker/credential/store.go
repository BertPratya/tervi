// Package credential stores verified pairing credentials and provides backends
// for credential files.
package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/state"
)

var (
	// ErrNotFound indicates that the credential file does not exist.
	ErrNotFound = errors.New("not found")
	// ErrDamaged indicates that an existing credential file is not pairing data.
	ErrDamaged = errors.New("damaged credential file")
	// ErrUnprotected indicates that the credential file is readable by another account.
	ErrUnprotected = errors.New("credential file is not private")
	// ErrStoreUnreadable indicates that a credential file could not be read.
	ErrStoreUnreadable = errors.New("credential file unreadable")
)

// Backend reads and writes raw credential values.
type Backend interface {
	Get(user string) (string, error)
	Set(user, value string) error
	Delete(user string) error
}

// Entry is a credential paired with the server that issued it.
type Entry struct {
	Server     string
	Credential secret.Value
}

type entryJSON struct {
	Server     string `json:"server"`
	Credential string `json:"credential"`
}

// Store applies the worker entry format and verification rules to a backend.
type Store struct{ backend Backend }

// NewStore creates credential storage logic over b.
func NewStore(b Backend) *Store { return &Store{backend: b} }

// Get reads and validates the worker entry. exists remains true for damaged data.
func (s *Store) Get() (entry Entry, exists bool, err error) {
	if s == nil || s.backend == nil {
		return Entry{}, false, errors.New("credential file backend unavailable")
	}
	raw, err := s.backend.Get("worker")
	if errors.Is(err, ErrNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("read credential file: %w", err)
	}
	var decoded entryJSON
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return Entry{}, true, ErrDamaged
	}
	standard, addressErr := state.StandardAddress(decoded.Server)
	if addressErr != nil || standard != decoded.Server || decoded.Credential == "" {
		return Entry{}, true, ErrDamaged
	}
	return Entry{Server: decoded.Server, Credential: secret.FromString(decoded.Credential)}, true, nil
}

// Save writes an entry and succeeds only when it reads back exactly as written.
func (s *Store) Save(entry Entry) error {
	if s == nil || s.backend == nil {
		return errors.New("credential file backend unavailable")
	}
	standard, err := state.StandardAddress(entry.Server)
	if err != nil || standard != entry.Server || entry.Credential.Reveal() == "" {
		return ErrDamaged
	}
	encoded, err := json.Marshal(entryJSON{Server: entry.Server, Credential: entry.Credential.Reveal()})
	if err != nil {
		return errors.New("encode credential file")
	}
	value := string(encoded)
	if err := s.backend.Set("worker", value); err != nil {
		return fmt.Errorf("write credential file: %w", err)
	}
	readBack, err := s.backend.Get("worker")
	if err != nil {
		return fmt.Errorf("verify credential file: %w", err)
	}
	if readBack != value {
		return errors.New("credential file verification did not match")
	}
	return nil
}

// Delete removes the worker entry. Deleting a missing entry is successful.
func (s *Store) Delete() error {
	if s == nil || s.backend == nil {
		return errors.New("credential file backend unavailable")
	}
	if err := s.backend.Delete("worker"); err != nil {
		return fmt.Errorf("delete credential file: %w", err)
	}
	return nil
}

// Check proves that the backend can store, read, compare, and delete a value.
func (s *Store) Check() error {
	return s.CheckContext(context.Background())
}

// CheckContext proves that the backend can store, read, compare, and delete a value unless ctx is cancelled.
func (s *Store) CheckContext(ctx context.Context) error {
	if s == nil || s.backend == nil {
		return errors.New("credential file backend unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	const user = checkCredentialUser
	const value = "tervi-credential-file-check"
	setErr := s.backend.Set(user, value)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	readBack, getErr := s.backend.Get(user)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	deleteErr := s.backend.Delete(user)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if setErr != nil || getErr != nil || readBack != value || deleteErr != nil {
		return errors.New("credential file check failed")
	}
	return nil
}
