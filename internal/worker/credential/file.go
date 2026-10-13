package credential

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const (
	workerCredentialUser = "worker"
	checkCredentialUser  = "worker-check"
)

type fileBackend struct {
	dir string
	uid int
}

// NewFileBackend stores the worker credential in a private file in dir.
func NewFileBackend(dir string) Backend { return newFileBackend(dir, os.Getuid()) }

func newFileBackend(dir string, uid int) Backend { return fileBackend{dir: dir, uid: uid} }

// Path returns the worker credential file path under dir.
func Path(dir string) string { return filepath.Join(dir, "credential.json") }

func (backend fileBackend) pathFor(user string) (string, error) {
	switch user {
	case workerCredentialUser:
		return Path(backend.dir), nil
	case checkCredentialUser:
		return filepath.Join(backend.dir, "credential-check.json"), nil
	default:
		return "", fmt.Errorf("unsupported credential file user %q", user)
	}
}

func (backend fileBackend) Get(user string) (string, error) {
	path, err := backend.pathFor(user)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("open credential file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect credential file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("credential file is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != backend.uid || info.Mode().Perm()&0o077 != 0 {
		return "", ErrUnprotected
	}
	value, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	return string(value), nil
}

func (backend fileBackend) Set(user, value string) error {
	path, err := backend.pathFor(user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(backend.dir, 0700); err != nil {
		return fmt.Errorf("create credential folder: %w", err)
	}
	temporary, err := os.CreateTemp(backend.dir, ".credential-*")
	if err != nil {
		return fmt.Errorf("write credential file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure credential file: %w", err)
	}
	if _, err := temporary.WriteString(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync credential file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close credential file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	return nil
}

func (backend fileBackend) Delete(user string) error {
	path, err := backend.pathFor(user)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete credential file: %w", err)
	}
	return nil
}
