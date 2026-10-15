package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const machineIDQuestion = "! tervi can't read this computer's machine ID (/etc/machine-id).\n" +
	"  Without it, the server won't recognize this computer if you pair it\n" +
	"  again later: it will come back as a new computer, without its history.\n" +
	"  To fix this, create a machine ID first: sudo systemd-machine-id-setup\n" +
	"Pair without it? [y/N]\n"

type machineIDTestFlow struct {
	hasMachineID bool
	startCalls   int
	finishCalls  int
	server       string
	startCode    int
	finishCode   int
}

func (f *machineIDTestFlow) HasMachineID() bool { return f.hasMachineID }

func (f *machineIDTestFlow) StartNew(_ context.Context, _ Env, server string) int {
	f.startCalls++
	f.server = server
	return f.startCode
}

func (f *machineIDTestFlow) FinishEarlier(context.Context, Env, credential.Entry) int {
	f.finishCalls++
	return f.finishCode
}

func TestNoMachineIDQuestion(t *testing.T) {
	t.Run("yes continues", func(t *testing.T) {
		env, stdout, stderr := machineIDTestEnv(t, strings.NewReader("  YeS  \n"))
		flow := &machineIDTestFlow{startCode: 1}
		if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
			t.Fatalf("Run() = %d, want 1 from the start flow", code)
		}
		if stdout.String() != machineIDQuestion || stderr.Len() != 0 || flow.startCalls != 1 {
			t.Errorf("output=%q stderr=%q start calls=%d", stdout.String(), stderr.String(), flow.startCalls)
		}
	})

	for _, tc := range []struct {
		name  string
		input io.Reader
	}{
		{name: "no", input: strings.NewReader("n\n")},
		{name: "empty", input: strings.NewReader("\n")},
		{name: "end of input", input: strings.NewReader("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := machineIDTestEnv(t, tc.input)
			before := prepareMachineIDLeftover(t, env)
			flow := &machineIDTestFlow{}
			if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
				t.Fatalf("Run() = %d, want 1", code)
			}
			if want := machineIDQuestion + "Nothing was changed.\n"; stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("output=%q stderr=%q, want %q", stdout.String(), stderr.String(), want)
			}
			if flow.startCalls != 0 || flow.finishCalls != 0 {
				t.Errorf("declining the question called the flow: %+v", flow)
			}
			after, err := os.ReadFile(state.StatePath(env.StateDir))
			if err != nil || !bytes.Equal(after, before) {
				t.Errorf("step zero changed state after decline: exists=%t", err == nil)
			}
			if _, exists, err := env.Store.Get(); err != nil || exists {
				t.Error("step zero changed the credential store after decline")
			}
		})
	}
}

func TestNoMachineIDYes(t *testing.T) {
	for _, args := range [][]string{
		{"pair", "--server", "http://localhost", "--yes"},
		{"pair", "--yes", "--server", "http://localhost"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			stdin := &machineIDTrapReader{}
			env, stdout, stderr := machineIDTestEnv(t, stdin)
			flow := &machineIDTestFlow{startCode: 1}
			if code := Run(context.Background(), args, env, flow); code != 1 {
				t.Fatalf("Run() = %d, want 1 from the start flow", code)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 || stdin.reads != 0 || flow.startCalls != 1 {
				t.Errorf("stdout=%q stderr=%q reads=%d starts=%d", stdout.String(), stderr.String(), stdin.reads, flow.startCalls)
			}
		})
	}
}

func TestPairAfterRevoked(t *testing.T) {
	env, _, _, backend, stdout, stderr := newPairHarness(t, false)
	if err := state.WriteState(env.StateDir, state.State{
		MachineID: "machine-123", CredentialSaved: true, Confirmed: true, StopReason: ptrString(state.StopReasonCredentialRevoked),
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Set("worker", `{"server":"http://localhost:8080","credential":"`+testCredential+`"}`); err != nil {
		t.Fatal(err)
	}
	flow := successfulPairFlow{inspectStart: true}
	if code := Run(context.Background(), []string{"pair", "--server", pairServer}, env, &flow); code != 0 {
		t.Fatalf("Run() = %d, output=%q", code, stdout.String())
	}
	wantPrefix := "This computer was unpaired on the server. Starting a new pairing.\n"
	if !strings.HasPrefix(stdout.String(), wantPrefix) || stderr.Len() != 0 || flow.starts != 1 {
		t.Errorf("output=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
	}
	if flow.inspectStartErr != nil || flow.stateAtStart || flow.entryAtStart {
		t.Errorf("old pairing still existed when replacement started: state=%v entry=%v err=%v", flow.stateAtStart, flow.entryAtStart, flow.inspectStartErr)
	}
	assertPairOutputSafe(t, stdout.String(), stderr.String())
}

func TestQuestionOnlyWhenStarting(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prepare    func(*testing.T, Env)
		hasID      bool
		wantPrompt bool
		wantStart  int
		wantFinish int
	}{
		{name: "already paired", prepare: func(t *testing.T, env Env) { prepareMachineIDPairing(t, env, true, "http://localhost") }},
		{name: "resume unfinished", prepare: func(t *testing.T, env Env) { prepareMachineIDPairing(t, env, false, "http://localhost") }, wantFinish: 1},
		{name: "other server unfinished", prepare: func(t *testing.T, env Env) { prepareMachineIDPairing(t, env, false, "https://other.example") }},
		{name: "no pairing yet", wantPrompt: true},
		{name: "leftovers", prepare: func(t *testing.T, env Env) { prepareMachineIDLeftover(t, env) }, wantPrompt: true},
		{name: "rejected", prepare: func(t *testing.T, env Env) { prepareMachineIDStopped(t, env, state.StopReasonCredentialRejected) }, wantPrompt: true},
		{name: "revoked", prepare: func(t *testing.T, env Env) { prepareMachineIDStopped(t, env, state.StopReasonCredentialRevoked) }, wantPrompt: true},
		{name: "machine ID present", hasID: true, wantStart: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, _ := machineIDTestEnv(t, strings.NewReader("n\n"))
			if tc.prepare != nil {
				tc.prepare(t, env)
			}
			flow := &machineIDTestFlow{hasMachineID: tc.hasID, startCode: 1}
			_ = Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow)
			if got := strings.Contains(stdout.String(), "Pair without it? [y/N]"); got != tc.wantPrompt {
				t.Errorf("asked machine ID question = %t, want %t; output=%q", got, tc.wantPrompt, stdout.String())
			}
			if flow.startCalls != tc.wantStart || flow.finishCalls != tc.wantFinish {
				t.Errorf("flow calls start=%d finish=%d, want %d and %d", flow.startCalls, flow.finishCalls, tc.wantStart, tc.wantFinish)
			}
		})
	}
}

type machineIDTrapReader struct{ reads int }

func (r *machineIDTrapReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("unexpected input read")
}

func machineIDTestEnv(t *testing.T, stdin io.Reader) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Env{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Store: credential.NewStore(credential.NewMemoryBackend()), StateDir: filepath.Join(t.TempDir(), "state"),
		ProgramPath: "/usr/bin/true",
	}, &stdout, &stderr
}

func prepareMachineIDLeftover(t *testing.T, env Env) []byte {
	t.Helper()
	if err := state.WriteState(env.StateDir, state.State{MachineID: "old-machine"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.StatePath(env.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	return before
}

func prepareMachineIDPairing(t *testing.T, env Env, confirmed bool, server string) {
	t.Helper()
	if err := state.WriteState(env.StateDir, state.State{MachineID: "old-machine", CredentialSaved: true, Confirmed: confirmed}); err != nil {
		t.Fatal(err)
	}
	if err := env.Store.Save(credential.Entry{Server: server, Credential: secretValue(testCredential)}); err != nil {
		t.Fatal(err)
	}
}

func prepareMachineIDStopped(t *testing.T, env Env, reason string) {
	t.Helper()
	if err := state.WriteState(env.StateDir, state.State{MachineID: "old-machine", CredentialSaved: true, Confirmed: true, StopReason: &reason}); err != nil {
		t.Fatal(err)
	}
	if err := env.Store.Save(credential.Entry{Server: "http://localhost", Credential: secretValue(testCredential)}); err != nil {
		t.Fatal(err)
	}
}

func secretValue(value string) secret.Value { return secret.FromString(value) }
