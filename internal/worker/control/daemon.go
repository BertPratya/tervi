package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Run takes the per-user lock, serves the control socket, and runs the engine
// until ctx ends. It returns ErrAlreadyRunning when another daemon holds the
// lock.
func Run(ctx context.Context, runtimeDir string, engine Engine) (runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if engine == nil {
		return errors.New("daemon engine is nil")
	}
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

	socketPath := filepath.Join(runtimeDir, "daemon.sock")
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old daemon socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on daemon socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) && runErr == nil {
			runErr = fmt.Errorf("remove daemon socket: %w", err)
		}
	}()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return fmt.Errorf("secure daemon socket: %w", err)
	}

	handlers := &handlerTracker{next: handler(engine)}
	server := &http.Server{Handler: handlers}
	peerListener := &sameUserListener{Listener: listener, uid: uint32(os.Getuid())}
	engineCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveDone := make(chan error, 1)
	serveExited := make(chan struct{})
	engineDone := make(chan error, 1)
	go func() {
		defer close(serveExited)
		serveDone <- server.Serve(peerListener)
	}()
	go func() { engineDone <- engine.Run(engineCtx) }()

	stopServer := func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		_ = server.Shutdown(shutdownCtx)
		shutdownCancel()
		_ = server.Close()
		<-serveExited
		handlers.stopAndWait()
	}
	select {
	case <-ctx.Done():
		stopServer()
		engineErr := <-engineDone
		serveErr := <-serveDone
		if engineErr != nil && !errors.Is(engineErr, context.Canceled) {
			return fmt.Errorf("daemon engine: %w", engineErr)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("daemon control server: %w", serveErr)
		}
		return nil
	case engineErr := <-engineDone:
		stopServer()
		serveErr := <-serveDone
		if engineErr != nil {
			return fmt.Errorf("daemon engine: %w", engineErr)
		}
		if ctx.Err() != nil {
			return nil
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("daemon control server: %w", serveErr)
		}
		return errors.New("daemon engine stopped unexpectedly")
	case serveErr := <-serveDone:
		stopServer()
		engineErr := <-engineDone
		if ctx.Err() != nil && (errors.Is(serveErr, http.ErrServerClosed) || serveErr == nil) && engineErr == nil {
			return nil
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("daemon control server: %w", serveErr)
		}
		if engineErr != nil {
			return fmt.Errorf("daemon engine: %w", engineErr)
		}
		return errors.New("daemon control server stopped unexpectedly")
	}
}

type handlerTracker struct {
	mu       sync.Mutex
	active   sync.WaitGroup
	stopping bool
	next     http.Handler
}

func (tracker *handlerTracker) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	tracker.mu.Lock()
	if tracker.stopping {
		tracker.mu.Unlock()
		return
	}
	tracker.active.Add(1)
	tracker.mu.Unlock()
	defer tracker.active.Done()
	tracker.next.ServeHTTP(w, request)
}

func (tracker *handlerTracker) stopAndWait() {
	tracker.mu.Lock()
	tracker.stopping = true
	tracker.mu.Unlock()
	tracker.active.Wait()
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
