package credential

import (
	"errors"
	"sync"
)

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
