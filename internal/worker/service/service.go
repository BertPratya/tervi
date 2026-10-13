// Package service manages tervi's systemd user service.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bertpratya/tervi/internal/worker/control"
)

const (
	serviceName    = "tervi"
	serviceFile    = "tervi.service"
	defaultPoll    = 200 * time.Millisecond
	defaultTimeout = 10 * time.Second
)

// Runner runs and streams operating system commands. Tests provide a fake so
// they never invoke systemd or journalctl.
type Runner interface {
	// Run runs a command to the end and returns its combined output.
	Run(ctx context.Context, name string, args ...string) (output string, err error)
	// Stream sends a command's standard output and standard error to out as they
	// are written, until it ends or ctx ends.
	Stream(ctx context.Context, out io.Writer, name string, args ...string) error
}

// ControlClient checks whether the daemon's private socket has started.
type ControlClient interface {
	Status(ctx context.Context) (control.StatusAnswer, error)
}

// Options are the local inputs needed to set up the background service.
type Options struct {
	Runner         Runner
	ProgramPath    string
	ServiceDir     string
	Client         ControlClient
	PollInterval   time.Duration
	StartupTimeout time.Duration
}

// Service manages tervi's systemd user service.
type Service struct{ options Options }

// New creates a service manager with the supplied command runner and paths.
func New(options Options) *Service { return &Service{options: options} }

// EnsureRunning writes or refreshes the systemd user service, enables and
// starts it, and waits for the daemon control socket to answer. It reports
// whether the service file was rewritten and the daemon restarted.
func (service *Service) EnsureRunning(ctx context.Context) (bool, error) {
	if service == nil {
		return false, errors.New("service options are missing")
	}
	options := service.options
	if options.Runner == nil {
		return false, errors.New("Runner is missing")
	}
	if options.ProgramPath == "" {
		return false, errors.New("ProgramPath is missing")
	}
	if options.ServiceDir == "" {
		return false, errors.New("ServiceDir is missing")
	}
	if options.Client == nil {
		return false, errors.New("ControlClient is missing")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	programPath, err := resolvedAbsolutePath(options.ProgramPath)
	if err != nil {
		return false, fmt.Errorf("resolve program path: %w", err)
	}
	path := filepath.Join(options.ServiceDir, serviceFile)
	contents, err := os.ReadFile(path)
	rewrite := errors.Is(err, os.ErrNotExist)
	if err != nil && !rewrite {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if !rewrite && serviceExecStart(contents) != programPath+" daemon" {
		rewrite = true
	}
	if rewrite {
		if err := writeServiceFile(options.ServiceDir, path, serviceContents(programPath)); err != nil {
			return false, err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", serviceName}, {"restart", serviceName}} {
			if err := runSystemctl(ctx, options.Runner, args...); err != nil {
				return false, err
			}
		}
	} else if err := runSystemctl(ctx, options.Runner, "enable", "--now", serviceName); err != nil {
		return false, err
	}
	if err := service.waitForDaemon(ctx, options); err != nil {
		return false, err
	}
	return rewrite, nil
}

func (service *Service) waitForDaemon(ctx context.Context, options Options) error {
	interval := options.PollInterval
	if interval <= 0 {
		interval = defaultPoll
	}
	timeout := options.StartupTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	startupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, err := options.Client.Status(startupCtx)
		if err == nil {
			return nil
		}
		if startupCtx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the daemon did not start within 10 seconds")
		}
		if !errors.Is(err, control.ErrNotRunning) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-startupCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the daemon did not start within 10 seconds")
		case <-ticker.C:
		}
	}
}

func runSystemctl(ctx context.Context, runner Runner, args ...string) error {
	commandArgs := append([]string{"--user"}, args...)
	output, err := runner.Run(ctx, "systemctl", commandArgs...)
	if err == nil {
		return nil
	}
	if message := strings.TrimSpace(output); message != "" {
		return errors.New(message)
	}
	return err
}

func serviceContents(programPath string) []byte {
	return []byte("[Unit]\n" +
		"Description=tervi worker\n\n" +
		"[Service]\n" +
		"ExecStart=\"" + escapeServicePath(programPath) + "\" daemon\n" +
		"Restart=on-failure\n" +
		"RestartSec=5\n\n" +
		"[Install]\n" +
		"WantedBy=default.target\n")
}

func serviceExecStart(contents []byte) string {
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecStart=") {
			return decodeServiceExecStart(strings.TrimPrefix(line, "ExecStart="))
		}
	}
	return ""
}

func escapeServicePath(path string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(path)
}

func decodeServiceExecStart(value string) string {
	const suffix = `" daemon`
	if !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, suffix) {
		return ""
	}
	quotedPath := strings.TrimSuffix(strings.TrimPrefix(value, `"`), suffix)
	path, err := strconv.Unquote(`"` + quotedPath + `"`)
	if err != nil {
		return ""
	}
	path = strings.ReplaceAll(path, "%%", "%")
	path = strings.ReplaceAll(path, "$$", "$")
	return path + " daemon"
}

func writeServiceFile(dir, path string, contents []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create service folder: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".tervi.service-*")
	if err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure service file: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write service file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync service file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close service file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace service file: %w", err)
	}
	return nil
}

func resolvedAbsolutePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// ExecRunner executes commands with os/exec.
type ExecRunner struct{}

// Run runs a command and returns its combined standard output and error.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	return string(output), err
}

// Stream sends a command's standard output and error to out as they are written.
func (ExecRunner) Stream(ctx context.Context, out io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = out
	command.Stderr = out
	return command.Run()
}
