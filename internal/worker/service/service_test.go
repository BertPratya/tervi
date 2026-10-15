package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/control"
)

type serviceTestRunner struct {
	calls   [][]string
	outputs map[string]string
	errors  map[string]error
	streams [][]string
	events  *[]string
}

func (r *serviceTestRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.events != nil {
		*r.events = append(*r.events, strings.Join(call, " "))
	}
	key := strings.Join(call, " ")
	return r.outputs[key], r.errors[key]
}

func (r *serviceTestRunner) Stream(_ context.Context, out io.Writer, name string, args ...string) error {
	r.streams = append(r.streams, append([]string{name}, args...))
	return nil
}

type serviceTestClient struct {
	answers []control.StatusAnswer
	errors  []error
	calls   int
	events  *[]string
}

func (c *serviceTestClient) Status(context.Context) (control.StatusAnswer, error) {
	i := c.calls
	c.calls++
	if c.events != nil {
		*c.events = append(*c.events, "status")
	}
	if i < len(c.errors) && c.errors[i] != nil {
		return control.StatusAnswer{}, c.errors[i]
	}
	if i < len(c.answers) {
		return c.answers[i], nil
	}
	return control.StatusAnswer{State: "reconnecting"}, nil
}

func serviceTestOptions(dir, program string, runner Runner, client *serviceTestClient) Options {
	return Options{
		Runner: runner, ProgramPath: program, ServiceDir: dir, Client: client,
		PollInterval: time.Millisecond, StartupTimeout: 20 * time.Millisecond,
	}
}

func TestCommandError(t *testing.T) {
	fallback := errors.New("command failed")
	for _, tc := range []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{name: "trimmed output", output: " systemd says no\n", err: fallback, want: "systemd says no"},
		{name: "fallback error", err: fallback, want: "command failed"},
		{name: "no error", output: "ignored"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := commandError(tc.output, tc.err)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("commandError() = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Error() != tc.want {
				t.Fatalf("commandError() = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestServiceFileContents(t *testing.T) {
	for _, name := range []string{"tervi program", `tervi\path`, `tervi"quote`, "tervi%percent", "tervi$dollar"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			program := filepath.Join(dir, name)
			if err := os.WriteFile(program, []byte("program"), 0700); err != nil {
				t.Fatal(err)
			}
			runner := &serviceTestRunner{}
			client := &serviceTestClient{answers: []control.StatusAnswer{{State: "connected"}}}
			if _, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background()); err != nil {
				t.Fatalf("EnsureRunning() error = %v", err)
			}
			resolved, err := filepath.EvalSymlinks(program)
			if err != nil {
				t.Fatal(err)
			}
			escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(resolved)
			contents, err := os.ReadFile(filepath.Join(dir, "tervi.service"))
			if err != nil {
				t.Fatal(err)
			}
			want := "[Unit]\nDescription=tervi worker\n\n[Service]\nExecStart=\"" + escaped + "\" daemon\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
			if string(contents) != want {
				t.Errorf("service file = %q, want %q", contents, want)
			}
		})
	}
}

func TestServiceFileRewrittenWhenPathChanges(t *testing.T) {
	dir := t.TempDir()
	runner := &serviceTestRunner{}
	client := &serviceTestClient{}
	path := filepath.Join(dir, "tervi.service")
	if err := os.WriteFile(path, []byte("[Service]\nExecStart=/old/tervi daemon\n"), 0600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, `tervi %\"$ program`)
	if err := os.WriteFile(program, []byte("program"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "tervi"}, {"systemctl", "--user", "restart", "tervi"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("systemctl calls = %#v, want %#v", runner.calls, want)
	}
	runner.calls = nil
	client.calls = 0
	if _, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	want = [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "enable", "--now", "tervi"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("same path calls = %#v, want %#v", runner.calls, want)
	}
	resolvedProgram, err := filepath.EvalSymlinks(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[Service]\nExecStart="+resolvedProgram+" daemon\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	client.calls = 0
	if _, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	want = [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "tervi"}, {"systemctl", "--user", "restart", "tervi"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("same-path unquoted ExecStart calls = %#v, want %#v", runner.calls, want)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(serviceContents(resolvedProgram)) {
		t.Errorf("rewritten service file = %q, want quoted contents %q", contents, serviceContents(resolvedProgram))
	}
}

func TestEnsureRunningReportsStoppedDaemonStarted(t *testing.T) {
	dir := t.TempDir()
	program := "/usr/bin/true"
	resolvedProgram, err := filepath.EvalSymlinks(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, serviceFile), serviceContents(resolvedProgram), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &serviceTestRunner{}
	client := &serviceTestClient{
		answers: []control.StatusAnswer{{State: "reconnecting"}},
		errors:  []error{control.ErrNotRunning},
	}

	started, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("EnsureRunning() error = %v", err)
	}
	if !started {
		t.Error("EnsureRunning() started = false, want true for a stopped daemon")
	}
	wantCalls := [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "enable", "--now", "tervi"}}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Errorf("systemctl calls = %#v, want %#v", runner.calls, wantCalls)
	}
	if client.calls != 2 {
		t.Errorf("Status calls = %d, want 2 (stopped check and readiness wait)", client.calls)
	}
}

func TestEnsureRunningSteps(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	runner := &serviceTestRunner{events: &events}
	client := &serviceTestClient{
		answers: []control.StatusAnswer{{}, {}, {State: "reconnecting"}},
		errors:  []error{control.ErrNotRunning, control.ErrNotRunning, errors.New("daemon answered with a broken response")},
		events:  &events,
	}
	if _, err := New(serviceTestOptions(dir, "/usr/bin/true", runner, client)).EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning() error = %v", err)
	}
	want := [][]string{{"loginctl", "enable-linger"}, {"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "tervi"}, {"systemctl", "--user", "restart", "tervi"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("systemctl calls = %#v, want %#v", runner.calls, want)
	}
	if client.calls != 3 {
		t.Errorf("Status calls = %d, want 3 including socket readiness wait", client.calls)
	}
	wantEvents := []string{"loginctl enable-linger", "systemctl --user daemon-reload", "systemctl --user enable tervi", "systemctl --user restart tervi", "status", "status", "status"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Errorf("systemd and socket readiness events = %#v, want %#v", events, wantEvents)
	}
}

func TestEnsureRunningTurnsOnLinger(t *testing.T) {
	for _, tc := range []struct {
		name           string
		currentService bool
		wantCalls      [][]string
	}{
		{
			name: "rewrite path",
			wantCalls: [][]string{
				{"loginctl", "enable-linger"},
				{"systemctl", "--user", "daemon-reload"},
				{"systemctl", "--user", "enable", "tervi"},
				{"systemctl", "--user", "restart", "tervi"},
			},
		},
		{
			name:           "enable now path",
			currentService: true,
			wantCalls: [][]string{
				{"loginctl", "enable-linger"},
				{"systemctl", "--user", "enable", "--now", "tervi"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			program := "/usr/bin/true"
			if tc.currentService {
				resolved, err := filepath.EvalSymlinks(program)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, serviceFile), serviceContents(resolved), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &serviceTestRunner{}
			client := &serviceTestClient{answers: []control.StatusAnswer{{State: "connected"}}}
			if _, err := New(serviceTestOptions(dir, program, runner, client)).EnsureRunning(context.Background()); err != nil {
				t.Fatalf("EnsureRunning() error = %v", err)
			}
			if !reflect.DeepEqual(runner.calls, tc.wantCalls) {
				t.Errorf("commands = %#v, want %#v", runner.calls, tc.wantCalls)
			}
		})
	}

	dir := t.TempDir()
	runner := &serviceTestRunner{
		outputs: map[string]string{"loginctl enable-linger": "linger is unavailable\n"},
		errors:  map[string]error{"loginctl enable-linger": errors.New("exit status 1")},
	}
	_, err := New(serviceTestOptions(dir, "/usr/bin/true", runner, &serviceTestClient{})).EnsureRunning(context.Background())
	if err == nil || err.Error() != "linger is unavailable" {
		t.Fatalf("EnsureRunning() error = %v, want linger output", err)
	}
	if want := [][]string{{"loginctl", "enable-linger"}}; !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("commands after linger failure = %#v, want only %#v", runner.calls, want)
	}
}

func TestEnsureRunningFailures(t *testing.T) {
	for _, tc := range []struct {
		name           string
		currentService bool
		outputs        map[string]string
		errors         map[string]error
		client         *serviceTestClient
		want           string
	}{
		{name: "systemctl output", outputs: map[string]string{"systemctl --user daemon-reload": "systemd says no\n"}, errors: map[string]error{"systemctl --user daemon-reload": errors.New("exit status 1")}, client: &serviceTestClient{}, want: "systemd says no"},
		{name: "systemd missing", errors: map[string]error{"systemctl --user daemon-reload": errors.New("exec: systemctl: executable file not found in $PATH")}, client: &serviceTestClient{}, want: "exec: systemctl: executable file not found in $PATH"},
		{name: "enable now output with current service", currentService: true, outputs: map[string]string{"systemctl --user enable --now tervi": "systemd says no\n"}, errors: map[string]error{"systemctl --user enable --now tervi": errors.New("exit status 1")}, client: &serviceTestClient{}, want: "systemd says no"},
		{name: "socket timeout", client: &serviceTestClient{errors: []error{control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning, control.ErrNotRunning}}, want: "the daemon did not start within 10 seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &serviceTestRunner{outputs: tc.outputs, errors: tc.errors}
			dir := t.TempDir()
			options := serviceTestOptions(dir, "/usr/bin/true", runner, tc.client)
			if tc.currentService {
				programPath, err := filepath.EvalSymlinks(options.ProgramPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, serviceFile), serviceContents(programPath), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "socket timeout" {
				options.StartupTimeout = 3 * time.Millisecond
			}
			_, err := New(options).EnsureRunning(context.Background())
			if err == nil || err.Error() != tc.want {
				t.Fatalf("EnsureRunning() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestServiceDirRequired(t *testing.T) {
	runner := &serviceTestRunner{}
	client := &serviceTestClient{}
	servicePath := filepath.Join(".", serviceFile)
	before, beforeErr := os.ReadFile(servicePath)
	_, err := New(serviceTestOptions("", "/usr/bin/true", runner, client)).EnsureRunning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ServiceDir") {
		t.Fatalf("EnsureRunning(empty ServiceDir) error = %v, want a ServiceDir error", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("systemctl called with empty ServiceDir: %#v", runner.calls)
	}
	after, afterErr := os.ReadFile(servicePath)
	if !errors.Is(beforeErr, os.ErrNotExist) && (afterErr != nil || !reflect.DeepEqual(before, after)) {
		t.Errorf("working-folder service file changed: before error=%v after error=%v", beforeErr, afterErr)
	}
	if errors.Is(beforeErr, os.ErrNotExist) && !errors.Is(afterErr, os.ErrNotExist) {
		t.Errorf("empty ServiceDir created %s", servicePath)
	}
}
