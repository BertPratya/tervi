package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
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
	path := credential.Path(filepath.Join(t.TempDir(), "state"))
	for _, tc := range []struct {
		name  string
		print func(io.Writer, string)
		want  string
	}{
		{name: "write", print: printUnusableStore, want: fmt.Sprintf("✗ Can't write %s.\n", path)},
		{name: "read", print: printStoreReadFailure, want: fmt.Sprintf("✗ Can't read %s.\n", path)},
		{name: "delete", print: printStoreDeleteFailure, want: fmt.Sprintf("✗ Can't delete %s.\n", path)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			tc.print(&output, path)
			if output.String() != tc.want {
				t.Errorf("message = %q, want %q", output.String(), tc.want)
			}
			if strings.Contains(strings.ToLower(output.String()), "keyring") {
				t.Errorf("message contains a keyring hint: %q", output.String())
			}
		})
	}
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
