// Package credential adapts the operating system keyring to the worker store.
package credential

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "tervi"

type keyringBackend struct{}

// NewKeyringBackend creates the operating system keyring backend used by tervi.
func NewKeyringBackend() Backend { return keyringBackend{} }

func (keyringBackend) Get(user string) (string, error) {
	value, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return value, err
}

func (keyringBackend) Set(user, value string) error {
	return keyring.Set(service, user, value)
}

func (keyringBackend) Delete(user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
