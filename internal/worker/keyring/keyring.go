// Package keyring adapts the operating system keyring to the worker store.
package keyring

import (
	"errors"

	"github.com/bertpratya/tervi/internal/worker/local"
	"github.com/zalando/go-keyring"
)

const service = "tervi"

type backend struct{}

// New returns the operating system keyring backend used by tervi.
func New() local.Backend { return backend{} }

func (backend) Get(user string) (string, error) {
	value, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", local.ErrNotFound
	}
	return value, err
}

func (backend) Set(user, value string) error {
	return keyring.Set(service, user, value)
}

func (backend) Delete(user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
