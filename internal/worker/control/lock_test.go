package control

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/daemon"
)

func TestWithLockBusy(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tervi")
	ctx, stop := context.WithCancel(context.Background())
	done := startDaemon(t, ctx, runtimeDir, &fakeEngine{status: daemon.Status{State: daemon.StateNotPaired}})
	waitForSocket(t, filepath.Join(runtimeDir, "daemon.sock"))
	var changed atomic.Bool
	err := WithLock(runtimeDir, func() error { changed.Store(true); return nil })
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("WithLock() error = %v, want ErrAlreadyRunning", err)
	}
	if changed.Load() {
		t.Fatal("WithLock ran change while daemon held the lock")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- WithLock(runtimeDir, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("WithLock did not acquire the free lock")
	}
	if err := Run(context.Background(), runtimeDir, &fakeEngine{status: daemon.Status{State: daemon.StateNotPaired}}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run() while WithLock owns lock = %v, want ErrAlreadyRunning", err)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
}
