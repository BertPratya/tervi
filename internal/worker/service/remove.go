package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileRemovalError identifies the service file that could not be removed.
type FileRemovalError struct {
	Path      string
	Err       error
	ReloadErr error
}

func (err *FileRemovalError) Error() string {
	message := fmt.Sprintf("remove %s: %v", err.Path, err.Err)
	if err.ReloadErr != nil {
		message += "; daemon-reload: " + err.ReloadErr.Error()
	}
	return message
}
func (err *FileRemovalError) Unwrap() error { return err.Err }

// ServiceFilePath returns the path to the user's tervi service file.
func ServiceFilePath(dir string) string { return filepath.Join(dir, serviceFile) }

// Stop stops the service and keeps it from starting at boot. A service that is
// already gone counts as stopped.
func (manager *Service) Stop(ctx context.Context) error {
	if manager == nil {
		return errors.New("service options are missing")
	}
	options := manager.options
	if options.Runner == nil {
		return errors.New("Runner is missing")
	}
	if options.ServiceDir == "" {
		return errors.New("ServiceDir is missing")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path := ServiceFilePath(options.ServiceDir)
	_, statErr := os.Lstat(path)
	if statErr == nil {
		output, err := options.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", serviceName)
		return removalCommandError(output, err)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", path, statErr)
	}
	output, err := options.Runner.Run(ctx, "systemctl", "--user", "stop", serviceName)
	if err == nil {
		return nil
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == 5 {
		return nil
	}
	return removalCommandError(output, err)
}

// RemoveFile removes the service file and reloads systemd. A file that is
// already gone counts as removed; daemon-reload is run in either case.
func (manager *Service) RemoveFile(ctx context.Context) error {
	if manager == nil {
		return errors.New("service options are missing")
	}
	options := manager.options
	if options.Runner == nil {
		return errors.New("Runner is missing")
	}
	if options.ServiceDir == "" {
		return errors.New("ServiceDir is missing")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path := ServiceFilePath(options.ServiceDir)
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	} else if removeErr != nil {
		removeErr = &FileRemovalError{Path: path, Err: removeErr}
	}
	output, reloadErr := options.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	reloadErr = removalCommandError(output, reloadErr)
	if removeErr != nil {
		var fileFailure *FileRemovalError
		if reloadErr != nil && errors.As(removeErr, &fileFailure) {
			fileFailure.ReloadErr = reloadErr
		}
		return removeErr
	}
	if reloadErr != nil {
		return reloadErr
	}
	return nil
}

// LingerOn reports whether linger is on for the calling user.
func LingerOn(ctx context.Context, runner Runner) (bool, error) {
	if runner == nil {
		return false, errors.New("Runner is missing")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := runner.Run(ctx, "loginctl", "show-user", "--property=Linger", "--value")
	if err != nil {
		return false, removalCommandError(output, err)
	}
	switch strings.TrimSpace(output) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, errors.New("loginctl returned an unexpected Linger value")
	}
}

func removalCommandError(output string, err error) error {
	if err == nil {
		return nil
	}
	if message := strings.TrimSpace(output); message != "" {
		return errors.New(message)
	}
	return err
}
