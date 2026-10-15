package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// WithLock takes daemon.lock without waiting and runs change while holding
// it. It returns ErrAlreadyRunning, without running change, when a daemon
// (or another holder) has the lock.
func WithLock(runtimeDir string, change func() error) error {
	if err := ensureRuntimeDir(runtimeDir); err != nil {
		return err
	}
	lock, err := acquireLock(filepath.Join(runtimeDir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	if change == nil {
		return errors.New("lock change is nil")
	}
	return change()
}

func acquireLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure daemon lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock daemon: %w", err)
	}
	return file, nil
}
