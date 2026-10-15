// Package unpair removes a worker pairing from the server and this computer.
package unpair

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/pair"
	"github.com/bertpratya/tervi/internal/worker/service"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultLockWait       = 10 * time.Second
	lockPollInterval      = 200 * time.Millisecond
	unpairPath            = "/api/v1/machines/current/unpair"
)

// Options controls unpair request and daemon-lock timeouts. Zero values use
// the production default of ten seconds.
type Options struct {
	RequestTimeout time.Duration
	LockWait       time.Duration
}

type unpairer struct {
	requestTimeout time.Duration
	lockWait       time.Duration
	client         *http.Client
}

// New creates the worker unpair operation.
func New(opts Options) cli.Unpairer {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = defaultRequestTimeout
	}
	if opts.LockWait <= 0 {
		opts.LockWait = defaultLockWait
	}
	return &unpairer{
		requestTimeout: opts.RequestTimeout,
		lockWait:       opts.LockWait,
		client:         pair.NewHTTPClient(),
	}
}

func (worker *unpairer) TellServer(ctx context.Context, entry credential.Entry) cli.UnpairAnswer {
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, worker.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, entry.Server+unpairPath, nil)
	if err != nil {
		return cli.UnpairAnswer{}
	}
	request.Header.Set("Authorization", "Bearer "+entry.Credential.Reveal())
	response, err := worker.client.Do(request)
	if err != nil {
		return cli.UnpairAnswer{}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusUnauthorized {
		return cli.UnpairAnswer{Done: true, Answered: true, Status: response.StatusCode}
	}
	return cli.UnpairAnswer{Answered: true, Status: response.StatusCode}
}

func (worker *unpairer) RemoveLocal(ctx context.Context, env cli.Env) *cli.RemoveFailure {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := service.New(service.Options{Runner: env.Runner, ServiceDir: env.ServiceDir})
	if err := manager.Stop(ctx); err != nil {
		return &cli.RemoveFailure{Background: true, Reason: err.Error()}
	}
	runtimeDir, err := control.RuntimeDir()
	if err != nil {
		return &cli.RemoveFailure{Background: true, Reason: err.Error()}
	}
	deadline := time.Now().Add(worker.lockWait)
	for {
		var failure *cli.RemoveFailure
		err := control.WithLock(runtimeDir, func() error {
			failure = removeLocked(ctx, manager, env)
			return nil
		})
		if err == nil {
			return failure
		}
		if !errors.Is(err, control.ErrAlreadyRunning) {
			return &cli.RemoveFailure{Background: true, Reason: err.Error()}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &cli.RemoveFailure{Background: true, Reason: "tervi is still running in the background"}
		}
		wait := lockPollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return &cli.RemoveFailure{Background: true, Reason: ctx.Err().Error()}
		case <-timer.C:
		}
	}
}

func removeLocked(ctx context.Context, manager *service.Service, env cli.Env) *cli.RemoveFailure {
	if err := manager.RemoveFile(ctx); err != nil {
		var fileFailure *service.FileRemovalError
		if errors.As(err, &fileFailure) {
			reason := removalReason(fileFailure.Err)
			if fileFailure.ReloadErr != nil {
				reason += "; daemon-reload: " + removalReason(fileFailure.ReloadErr)
			}
			return &cli.RemoveFailure{Path: fileFailure.Path, Reason: reason}
		}
		return &cli.RemoveFailure{Background: true, Reason: err.Error()}
	}
	if env.Store == nil {
		return &cli.RemoveFailure{Path: credential.Path(env.StateDir), Reason: "credential store is unavailable"}
	}
	if err := env.Store.Delete(); err != nil {
		return &cli.RemoveFailure{Path: credential.Path(env.StateDir), Reason: removalReason(err)}
	}
	if err := credential.DeleteLeftovers(env.StateDir); err != nil {
		var leftoverFailure *credential.LeftoverRemovalError
		if errors.As(err, &leftoverFailure) {
			return &cli.RemoveFailure{Path: leftoverFailure.Path, Reason: removalReason(leftoverFailure.Err)}
		}
		return &cli.RemoveFailure{Path: env.StateDir, Reason: removalReason(err)}
	}
	if err := state.DeleteState(env.StateDir); err != nil {
		return &cli.RemoveFailure{Path: state.StatePath(env.StateDir), Reason: removalReason(err)}
	}
	return nil
}

func removalReason(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func (worker *unpairer) LingerOn(ctx context.Context, env cli.Env) bool {
	on, err := service.LingerOn(ctx, env.Runner)
	return err == nil && on
}

var _ cli.Unpairer = (*unpairer)(nil)
