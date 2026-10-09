package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/bertpratya/tervi/internal/secret"
)

var (
	// ErrNotFound indicates that the secret store has no value for the user.
	ErrNotFound = errors.New("not found")
	// ErrDamaged indicates that an existing secret store value is not pairing data.
	ErrDamaged = errors.New("damaged secret store entry")
)

// Backend reads and writes raw values under the tervi service.
type Backend interface {
	Get(user string) (string, error)
	Set(user, value string) error
	Delete(user string) error
}

// MemoryBackend is an in-memory secret store backend for worker tests.
type MemoryBackend struct {
	FailGet, FailSet, FailDelete bool
	ChangeOnRead                 bool

	mu     sync.RWMutex
	values map[string]string
}

// NewMemoryBackend creates an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{values: make(map[string]string)}
}

func (m *MemoryBackend) Get(user string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.FailGet {
		return "", errors.New("secret store get failed")
	}
	value, ok := m.values[user]
	if !ok {
		return "", ErrNotFound
	}
	if m.ChangeOnRead {
		return value + " changed on read", nil
	}
	return value, nil
}

func (m *MemoryBackend) Set(user, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailSet {
		return errors.New("secret store set failed")
	}
	if m.values == nil {
		m.values = make(map[string]string)
	}
	m.values[user] = value
	return nil
}

func (m *MemoryBackend) Delete(user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailDelete {
		return errors.New("secret store delete failed")
	}
	delete(m.values, user)
	return nil
}

// Value returns the raw value stored for user.
func (m *MemoryBackend) Value(user string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.values[user]
	return value, ok
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

// NewStore creates secret store logic over b.
func NewStore(b Backend) *Store { return &Store{backend: b} }

// Get reads and validates the worker entry. exists remains true for damaged data.
func (s *Store) Get() (entry Entry, exists bool, err error) {
	if s == nil || s.backend == nil {
		return Entry{}, false, errors.New("secret store backend unavailable")
	}
	raw, err := s.backend.Get("worker")
	if errors.Is(err, ErrNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("read secret store entry: %w", err)
	}
	var decoded entryJSON
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return Entry{}, true, ErrDamaged
	}
	standard, addressErr := StandardAddress(decoded.Server)
	if addressErr != nil || standard != decoded.Server || decoded.Credential == "" {
		return Entry{}, true, ErrDamaged
	}
	return Entry{Server: decoded.Server, Credential: secret.FromString(decoded.Credential)}, true, nil
}

// Save writes an entry and succeeds only when it reads back exactly as written.
func (s *Store) Save(entry Entry) error {
	if s == nil || s.backend == nil {
		return errors.New("secret store backend unavailable")
	}
	standard, err := StandardAddress(entry.Server)
	if err != nil || standard != entry.Server || entry.Credential.Reveal() == "" {
		return ErrDamaged
	}
	encoded, err := json.Marshal(entryJSON{Server: entry.Server, Credential: entry.Credential.Reveal()})
	if err != nil {
		return errors.New("encode secret store entry")
	}
	value := string(encoded)
	if err := s.backend.Set("worker", value); err != nil {
		return fmt.Errorf("write secret store entry: %w", err)
	}
	readBack, err := s.backend.Get("worker")
	if err != nil {
		return fmt.Errorf("verify secret store entry: %w", err)
	}
	if readBack != value {
		return errors.New("secret store entry verification did not match")
	}
	return nil
}

// Delete removes the worker entry. Deleting a missing entry is successful.
func (s *Store) Delete() error {
	if s == nil || s.backend == nil {
		return errors.New("secret store backend unavailable")
	}
	if err := s.backend.Delete("worker"); err != nil {
		return fmt.Errorf("delete secret store entry: %w", err)
	}
	return nil
}

// Check proves that the backend can store, read, compare, and delete a value.
func (s *Store) Check() error {
	return s.check(context.Background())
}

func (s *Store) check(ctx context.Context) error {
	if s == nil || s.backend == nil {
		return errors.New("secret store backend unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	const user = "worker-check"
	const value = "tervi-secret-store-check"
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
		return errors.New("secret store check failed")
	}
	return nil
}
