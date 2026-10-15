package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bertpratya/tervi/internal/worker/control"
)

type removeExitError struct{ code int }

func (err removeExitError) Error() string { return "command failed" }
func (err removeExitError) ExitCode() int { return err.code }

func TestRemoveServiceAlreadyGone(t *testing.T) {
	dir := t.TempDir()
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	runner := &serviceTestRunner{errors: map[string]error{
		"systemctl --user stop tervi": removeExitError{code: 5},
	}}
	manager := New(Options{Runner: runner, ServiceDir: dir})
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want status 5 to count as stopped", err)
	}
	if err := manager.RemoveFile(context.Background()); err != nil {
		t.Fatalf("RemoveFile() error = %v", err)
	}
	want := [][]string{{"systemctl", "--user", "stop", "tervi"}, {"systemctl", "--user", "daemon-reload"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("runner calls = %#v, want %#v", runner.calls, want)
	}
	if err := control.WithLock(filepath.Join(runtime, "tervi"), func() error { return nil }); err != nil {
		t.Errorf("runtime lock was unexpectedly held: %v", err)
	}
}

func TestRemoveServiceStopAndFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serviceFile)
	if err := os.WriteFile(path, []byte("service"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &serviceTestRunner{errors: map[string]error{
		"systemctl --user disable --now tervi": errors.New("disable failed"),
	}}
	manager := New(Options{Runner: runner, ServiceDir: dir})
	if err := manager.Stop(context.Background()); err == nil {
		t.Fatal("Stop() error = nil, want command error")
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], []string{"systemctl", "--user", "disable", "--now", "tervi"}) {
		t.Errorf("runner calls = %#v", runner.calls)
	}
	if err := manager.RemoveFile(context.Background()); err != nil {
		t.Fatalf("RemoveFile() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("service file remains: %v", err)
	}
	if !reflect.DeepEqual(runner.calls[len(runner.calls)-1], []string{"systemctl", "--user", "daemon-reload"}) {
		t.Errorf("last runner call = %#v, want daemon-reload", runner.calls[len(runner.calls)-1])
	}
}

func TestLingerOn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  string
		err     error
		want    bool
		wantErr bool
	}{
		{name: "yes", output: "yes\n", want: true},
		{name: "no", output: "no\n"},
		{name: "runner failure", err: errors.New("loginctl failed"), wantErr: true},
		{name: "unexpected value", output: "maybe", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &serviceTestRunner{outputs: map[string]string{"loginctl show-user --property=Linger --value": tc.output}, errors: map[string]error{"loginctl show-user --property=Linger --value": tc.err}}
			got, err := LingerOn(context.Background(), runner)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("LingerOn() = (%v, %v), want (%v, error=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
