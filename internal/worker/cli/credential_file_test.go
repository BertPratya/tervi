package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

func TestPairUnwritableCredentialStopsBeforeServer(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the state folder write mode")
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0700) })

	var stdout, stderr bytes.Buffer
	runner := &commandRunner{}
	client := &commandControl{}
	env := Env{
		Stdout: &stdout, Stderr: &stderr,
		Store:    credential.NewStore(credential.NewFileBackend(stateDir)),
		StateDir: stateDir, Runner: runner, ProgramPath: "/usr/bin/true", ServiceDir: t.TempDir(),
		ControlClient: client,
	}
	flow := &flowRecorder{}
	if code := Run(context.Background(), []string{"pair", "--server", "http://localhost:8080"}, env, flow); code != 1 {
		t.Fatalf("Run() = %d, want 1; stdout=%q", code, stdout.String())
	}
	want := fmt.Sprintf("✗ Can't write %s.\n", credential.Path(stateDir))
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
	}
	if flow.startCalls != 0 || flow.finishCalls != 0 || client.statusCalls != 0 || client.connectCalls != 0 || len(runner.calls) != 0 {
		t.Errorf("pair contacted the server or service: flow=%+v control=%+v runner=%v", flow, client, runner.calls)
	}
	if _, err := os.Stat(credential.Path(stateDir)); !os.IsNotExist(err) {
		t.Errorf("credential file exists after failed preflight: %v", err)
	}
}

func TestCredentialFileMessages(t *testing.T) {
	type messageCase struct {
		name  string
		setup func(t *testing.T, env *Env, stateDir string, flow *flowRecorder)
		want  func(string) string
		code  int
	}
	cases := []messageCase{
		{
			name: "write",
			setup: func(_ *testing.T, env *Env, _ string, _ *flowRecorder) {
				env.Store = credential.NewStore(failingCredentialCheckBackend{Backend: credential.NewFileBackend(env.StateDir)})
			},
			want: func(path string) string { return fmt.Sprintf("✗ Can't write %s.\n", path) },
			code: 1,
		},
		{
			name: "read",
			setup: func(t *testing.T, _ *Env, stateDir string, _ *flowRecorder) {
				if err := os.Mkdir(credential.Path(stateDir), 0700); err != nil {
					t.Fatal(err)
				}
			},
			want: func(path string) string { return fmt.Sprintf("✗ Can't read %s.\n", path) },
			code: 1,
		},
		{
			name: "delete",
			setup: func(t *testing.T, env *Env, stateDir string, _ *flowRecorder) {
				if err := os.WriteFile(credential.Path(stateDir), []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
				env.Store = credential.NewStore(failingCredentialDeleteBackend{Backend: credential.NewFileBackend(stateDir)})
			},
			want: func(path string) string { return fmt.Sprintf("✗ Can't delete %s.\n", path) },
			code: 1,
		},
		{
			name: "damaged",
			setup: func(t *testing.T, _ *Env, stateDir string, flow *flowRecorder) {
				if err := os.WriteFile(credential.Path(stateDir), []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
				flow.startCode = 1
			},
			want: func(path string) string {
				return fmt.Sprintf("Found a damaged %s.\n  Removed the leftover pairing data. Starting a new pairing.\n", path)
			},
			code: 1,
		},
		{
			name: "incomplete credential without state",
			setup: func(t *testing.T, env *Env, _ string, flow *flowRecorder) {
				if err := env.Store.Save(credential.Entry{Server: "http://localhost:8080", Credential: testSecretValue()}); err != nil {
					t.Fatal(err)
				}
				flow.startCode = 1
			},
			want: func(_ string) string {
				return "Found incomplete pairing data: credential.json exists, but state.json is missing.\n  Removed the leftover pairing data. Starting a new pairing.\n"
			},
			code: 1,
		},
		{
			name: "incomplete state without credential",
			setup: func(t *testing.T, env *Env, _ string, flow *flowRecorder) {
				if err := state.WriteState(env.StateDir, state.State{MachineID: "machine", CredentialSaved: true}); err != nil {
					t.Fatal(err)
				}
				flow.startCode = 1
			},
			want: func(_ string) string {
				return "Found incomplete pairing data: state.json exists, but credential.json is missing.\n  Removed the leftover pairing data. Starting a new pairing.\n"
			},
			code: 1,
		},
		{
			name: "unprotected",
			setup: func(t *testing.T, env *Env, _ string, _ *flowRecorder) {
				if err := env.Store.Save(credential.Entry{Server: "http://localhost:8080", Credential: testSecretValue()}); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(credential.Path(env.StateDir), 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: func(path string) string {
				return fmt.Sprintf("✗ Other users may be able to read %s, so tervi won't use it.\n  Make it private with: chmod 600 %s\n", path, path)
			},
			code: 1,
		},
		{
			name: "background started",
			want: func(_ string) string {
				return "✓ Paired successfully.\n✓ tervi now runs in the background and starts when the computer starts.\n✓ Connected.\n"
			},
			code: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			var stdout, stderr bytes.Buffer
			runner := &commandRunner{}
			env := Env{
				Stdout: &stdout, Stderr: &stderr,
				Store: credential.NewStore(credential.NewFileBackend(stateDir)), StateDir: stateDir,
				Runner: runner, ProgramPath: "/usr/bin/true", ServiceDir: t.TempDir(),
				ControlClient: &commandControl{connect: control.ConnectAnswer{Result: "connected"}},
			}
			flow := &flowRecorder{}
			if tc.setup != nil {
				tc.setup(t, &env, stateDir, flow)
			}
			if tc.name == "background started" {
				t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			}
			if code := Run(context.Background(), []string{"pair", "--server", "http://localhost:8080"}, env, flow); code != tc.code {
				t.Fatalf("Run() = %d, want %d; stdout=%q stderr=%q", code, tc.code, stdout.String(), stderr.String())
			}
			if got, want := stdout.String(), tc.want(credential.Path(stateDir)); got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			output := strings.ToLower(stdout.String() + stderr.String())
			for _, forbidden := range []string{"keyring", "secret store"} {
				if strings.Contains(output, forbidden) {
					t.Errorf("worker output contains %q: %q", forbidden, output)
				}
			}
		})
	}
}

type failingCredentialCheckBackend struct{ credential.Backend }

func (backend failingCredentialCheckBackend) Set(user, value string) error {
	if user == "worker-check" {
		return errors.New("credential file check failed")
	}
	return backend.Backend.Set(user, value)
}

type failingCredentialDeleteBackend struct{ credential.Backend }

func (backend failingCredentialDeleteBackend) Delete(user string) error {
	if user == "worker" {
		return errors.New("credential file delete failed")
	}
	return backend.Backend.Delete(user)
}

func TestUnprotectedCredentialMessages(t *testing.T) {
	for _, command := range []string{"pair", "status", "connect"} {
		t.Run(command, func(t *testing.T) {
			stateDir := t.TempDir()
			if err := state.WriteState(stateDir, state.State{MachineID: "machine", CredentialSaved: true, Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			store := credential.NewStore(credential.NewFileBackend(stateDir))
			if err := store.Save(credential.Entry{Server: "http://localhost:8080", Credential: testSecretValue()}); err != nil {
				t.Fatal(err)
			}
			path := credential.Path(stateDir)
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

			var stdout, stderr bytes.Buffer
			runner := &commandRunner{}
			client := &commandControl{}
			env := Env{
				Stdout: &stdout, Stderr: &stderr, Store: store, StateDir: stateDir,
				Runner: runner, ProgramPath: "/usr/bin/true", ServiceDir: t.TempDir(), ControlClient: client,
			}
			flow := &flowRecorder{}
			args := []string{command}
			if command == "pair" {
				args = []string{"pair", "--server", "http://localhost:8080"}
			}
			if code := Run(context.Background(), args, env, flow); code != 1 {
				t.Fatalf("Run(%v) = %d, want 1; output=%q", args, code, stdout.String())
			}
			want := fmt.Sprintf("✗ Other users may be able to read %s, so tervi won't use it.\n  Make it private with: chmod 600 %s\n", path, path)
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("output=(%q,%q), want %q", stdout.String(), stderr.String(), want)
			}
			if flow.startCalls != 0 || flow.finishCalls != 0 || client.statusCalls != 0 || client.connectCalls != 0 || len(runner.calls) != 0 {
				t.Errorf("unprotected credential reached server/daemon/service: flow=%+v control=%+v runner=%v", flow, client, runner.calls)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0644 {
				t.Errorf("credential file after refusal: info=%v err=%v, want mode 0644 unchanged", info, err)
			}
		})
	}
}

func testSecretValue() secret.Value { return secret.FromString(testCredential) }
