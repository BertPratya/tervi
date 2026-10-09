package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const testCredential = "credential-must-never-be-printed"

type flowRecorder struct {
	startCalls  int
	finishCalls int
	server      string
	entry       credential.Entry
	startCode   int
	finishCode  int
}

func (f *flowRecorder) StartNew(_ context.Context, _ Env, server string) int {
	f.startCalls++
	f.server = server
	return f.startCode
}

func (f *flowRecorder) FinishEarlier(_ context.Context, _ Env, entry credential.Entry) int {
	f.finishCalls++
	f.entry = entry
	return f.finishCode
}

type trapReader struct{ reads int }

func (r *trapReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("unexpected read")
}

type countingBackend struct {
	gets, sets, deletes int
}

func (b *countingBackend) Get(string) (string, error) {
	b.gets++
	return "", credential.ErrNotFound
}

func (b *countingBackend) Set(string, string) error {
	b.sets++
	return nil
}

func (b *countingBackend) Delete(string) error {
	b.deletes++
	return nil
}

func usageEnv(t *testing.T) (Env, *countingBackend, *trapReader, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	backend := &countingBackend{}
	store := credential.NewStore(backend)
	stdin := &trapReader{}
	var stdout, stderr bytes.Buffer
	return Env{Stdin: stdin, Stdout: &stdout, Stderr: &stderr, Store: store, StateDir: filepath.Join(t.TempDir(), "state")}, backend, stdin, &stdout, &stderr
}

func TestUsageErrors(t *testing.T) {
	usage := "Usage: tervi pair --server <url>\n"
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, usage},
		{"no server", []string{"pair"}, usage},
		{"server missing value", []string{"pair", "--server"}, usage},
		{"server empty equals", []string{"pair", "--server="}, usage},
		{"server followed by flag", []string{"pair", "--server", "--foo"}, usage},
		{"server repeated", []string{"pair", "--server", "http://a", "--server", "http://b"}, usage},
		{"invalid server", []string{"pair", "--server", "banana"}, "Invalid server address: banana\n" + usage},
		{"unknown flag", []string{"pair", "-server", "http://a"}, "Unknown flag: -server\n" + usage},
		{"flag before command", []string{"--server", "x", "pair"}, "Unknown command: --server\n" + usage},
		{"unknown argument", []string{"pair", "extra"}, "Unknown argument: extra\n" + usage},
		{"unknown command", []string{"other"}, "Unknown command: other\n" + usage},
		{"short help after pair", []string{"pair", "-h"}, usage},
		{"help after pair", []string{"pair", "--help"}, usage},
		{"help before pair", []string{"--help", "pair"}, usage},
		{"help wins", []string{"pair", "--bogus", "--help", "--server=banana"}, usage},
		{"leftmost error", []string{"pair", "--bogus", "--server=banana"}, "Unknown flag: --bogus\n" + usage},
		{"extra argument before missing server", []string{"pair", "extra"}, "Unknown argument: extra\n" + usage},
		{"extra argument after server", []string{"pair", "--server", "http://localhost", "extra"}, "Unknown argument: extra\n" + usage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, backend, stdin, stdout, stderr := usageEnv(t)
			flow := &flowRecorder{}
			if got := Run(context.Background(), tc.args, env, flow); got != 2 {
				t.Fatalf("Run() = %d, want 2", got)
			}
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout = %q, want empty", got)
			}
			if stdin.reads != 0 || flow.startCalls != 0 || flow.finishCalls != 0 {
				t.Errorf("usage handling did work: reads=%d flow=%+v", stdin.reads, flow)
			}
			if backend.gets != 0 || backend.sets != 0 || backend.deletes != 0 {
				t.Errorf("usage handling touched secret store: %+v", backend)
			}
			if _, err := os.Stat(env.StateDir); !os.IsNotExist(err) {
				t.Errorf("usage handling touched state directory: stat error %v", err)
			}
		})
	}
	for _, args := range [][]string{{"pair", "--server=http://localhost"}, {"pair", "--server", "http://localhost"}} {
		var stdout, stderr bytes.Buffer
		env := Env{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Store: credential.NewStore(credential.NewMemoryBackend()), StateDir: filepath.Join(t.TempDir(), "state")}
		flow := &flowRecorder{}
		if code := Run(context.Background(), args, env, flow); code != 0 {
			t.Errorf("Run(%q) = %d, want 0", args, code)
		}
		if flow.startCalls != 1 || flow.server != "http://localhost" {
			t.Errorf("flow = %+v, want one start at standard address", flow)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestStateFileBroken(t *testing.T) {
	dir := t.TempDir()
	path := state.StatePath(dir)
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	backend := credential.NewMemoryBackend()
	env, stdout, stderr := runEnv(t, dir, backend)
	flow := &flowRecorder{}
	if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}
	want := fmt.Sprintf("✗ Can't read %s.\n", path)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 || flow.startCalls != 0 || flow.finishCalls != 0 {
		t.Errorf("stderr=%q flow=%+v", stderr.String(), flow)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Errorf("broken state changed from %q to %q", before, after)
	}
}

func TestStepZero(t *testing.T) {
	server := "http://localhost"
	entryRaw := func(server string) string {
		data, _ := json.Marshal(map[string]string{"server": server, "credential": testCredential})
		return string(data)
	}
	baseMessage := "  Removed the leftover pairing data. Starting a new pairing.\n"
	cases := []struct {
		name            string
		state           *state.State
		entry           string
		wantOut         string
		wantCode        int
		start           int
		finish          int
		wantState       *state.State
		wantStateExists bool
		wantStateSame   bool
		wantEntry       string
		wantEntryExists bool
	}{
		{name: "missing both", wantCode: 0, start: 1},
		{name: "missing state", entry: entryRaw(server), wantOut: "Found incomplete pairing data: the secret store entry exists, but state.json is missing.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "saved false but entry missing", state: &state.State{MachineID: "m"}, wantOut: "The earlier pairing was interrupted before the credential was saved.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "saved false and entry exists", state: &state.State{MachineID: "m"}, entry: entryRaw(server), wantCode: 0, finish: 1, wantState: &state.State{MachineID: "m", CredentialSaved: true}, wantStateExists: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "saved true and entry exists", state: &state.State{MachineID: "m", CredentialSaved: true}, entry: entryRaw(server), wantCode: 0, finish: 1, wantState: &state.State{MachineID: "m", CredentialSaved: true}, wantStateExists: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "saved true but entry missing", state: &state.State{MachineID: "m", CredentialSaved: true}, wantOut: "Found incomplete pairing data: state.json exists, but the secret store entry is missing.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "confirmed", state: &state.State{MachineID: "m", CredentialSaved: true, Confirmed: true}, entry: entryRaw(server), wantOut: "This computer is already paired with http://localhost. Nothing was changed.\n", wantCode: 1, wantState: &state.State{MachineID: "m", CredentialSaved: true, Confirmed: true}, wantStateExists: true, wantStateSame: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "damaged entry", state: &state.State{MachineID: "m"}, entry: "damaged json", wantOut: "Found a damaged secret store entry for tervi.\n" + baseMessage, wantCode: 0, start: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			backend := credential.NewMemoryBackend()
			if tc.state != nil {
				if err := state.WriteState(dir, *tc.state); err != nil {
					t.Fatal(err)
				}
			}
			if tc.entry != "" {
				if err := backend.Set("worker", tc.entry); err != nil {
					t.Fatal(err)
				}
			}
			var stateBefore []byte
			if tc.state != nil {
				var err error
				stateBefore, err = os.ReadFile(state.StatePath(dir))
				if err != nil {
					t.Fatal(err)
				}
			}
			env, stdout, stderr := runEnv(t, dir, backend)
			flow := &flowRecorder{}
			code := Run(context.Background(), []string{"pair", "--server", "HTTP://LOCALHOST/"}, env, flow)
			if code != tc.wantCode || flow.startCalls != tc.start || flow.finishCalls != tc.finish {
				t.Errorf("Run()=%d flow=%+v; want code=%d start=%d finish=%d", code, flow, tc.wantCode, tc.start, tc.finish)
			}
			if flow.startCalls > 0 && flow.server != server {
				t.Errorf("StartNew server = %q, want %q", flow.server, server)
			}
			if flow.finishCalls > 0 && (flow.entry.Server != server || flow.entry.Credential.Reveal() != testCredential) {
				t.Errorf("FinishEarlier received an unexpected entry")
			}
			if got := stdout.String(); got != tc.wantOut {
				t.Errorf("stdout = %q, want %q", got, tc.wantOut)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			record, stateExists, stateErr := state.ReadState(dir)
			if stateErr != nil {
				t.Fatalf("ReadState() error = %v", stateErr)
			}
			if stateExists != tc.wantStateExists {
				t.Errorf("state exists = %v, want %v", stateExists, tc.wantStateExists)
			}
			if tc.wantStateExists && tc.wantState != nil && record != *tc.wantState {
				t.Errorf("state after run = %+v, want %+v", record, *tc.wantState)
			}
			if tc.wantStateSame {
				stateAfter, err := os.ReadFile(state.StatePath(dir))
				if err != nil {
					t.Fatalf("reading unchanged state: %v", err)
				}
				if !bytes.Equal(stateBefore, stateAfter) {
					t.Errorf("state changed: before=%q after=%q", stateBefore, stateAfter)
				}
			}
			entryAfter, entryExists := backend.Value("worker")
			if tc.wantEntryExists != entryExists {
				t.Errorf("entry exists = %v, want %v", entryExists, tc.wantEntryExists)
			}
			if tc.wantEntryExists && entryAfter != tc.wantEntry {
				t.Errorf("entry after run = %q, want %q", entryAfter, tc.wantEntry)
			}
			assertNoCredential(t, stdout.String(), stderr.String())
		})
	}
}

func TestStepZeroOtherServerChangesNothing(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("credential_saved_%v", saved), func(t *testing.T) {
			dir := t.TempDir()
			record := state.State{MachineID: "m", CredentialSaved: saved}
			if err := state.WriteState(dir, record); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(state.StatePath(dir))
			backend := credential.NewMemoryBackend()
			if err := backend.Set("worker", `{"server":"https://other.example","credential":"`+testCredential+`"}`); err != nil {
				t.Fatal(err)
			}
			env, stdout, stderr := runEnv(t, dir, backend)
			flow := &flowRecorder{}
			if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
				t.Fatalf("Run() = %d, want 1", code)
			}
			want := "✗ A pairing with https://other.example isn't finished yet.\n  To finish it, run:  tervi pair --server https://other.example\n"
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("stdout=%q stderr=%q, want %q and empty", stdout.String(), stderr.String(), want)
			}
			after, _ := os.ReadFile(state.StatePath(dir))
			if !bytes.Equal(before, after) {
				t.Errorf("state changed: before=%q after=%q", before, after)
			}
			if flow.finishCalls != 0 || flow.startCalls != 0 {
				t.Errorf("unexpected flow calls: %+v", flow)
			}
			if _, ok := backend.Value("worker"); !ok {
				t.Error("secret store entry changed")
			}
			assertNoCredential(t, stdout.String(), stderr.String())
		})
	}
}

func TestUnusableStoreStopsBeforeFlow(t *testing.T) {
	backend := credential.NewMemoryBackend()
	backend.FailSet = true
	env, stdout, stderr := runEnv(t, t.TempDir(), backend)
	flow := &flowRecorder{}
	if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}
	want := "✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.\n  Make sure you are logged in to a desktop session and the keyring is unlocked.\n"
	if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 0 {
		t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
	}
}

func TestStateWriteFailsInStepZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are bypassed for the root user")
	}
	backend := credential.NewMemoryBackend()
	if err := backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "read-only")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteState(dir, state.State{MachineID: "m", CredentialSaved: false}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	env, stdout, stderr := runEnv(t, dir, backend)
	flow := &flowRecorder{}
	code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow)
	if code != 1 || stdout.String() != fmt.Sprintf("✗ Can't write %s.\n", state.StatePath(dir)) || stderr.Len() != 0 || flow.finishCalls != 0 {
		t.Errorf("code=%d stdout=%q stderr=%q flow=%+v", code, stdout.String(), stderr.String(), flow)
	}
}

func TestCtrlCDuringStepZero(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := credential.NewMemoryBackend()
		env, stdout, stderr := runEnv(t, t.TempDir(), backend)
		flow := &flowRecorder{}
		if code := Run(ctx, []string{"pair", "--server", "http://localhost"}, env, flow); code != 130 {
			t.Fatalf("Run() = %d, want 130", code)
		}
		if stdout.String() != "Pairing cancelled. Nothing was saved.\n" || stderr.Len() != 0 || flow.startCalls+flow.finishCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
	})
	t.Run("cancelled during store read after finding resumable entry", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{MachineID: "m", CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		backend := &cancelGetBackend{MemoryBackend: credential.NewMemoryBackend(), cancel: cancel}
		_ = backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`)
		env, stdout, stderr := runEnv(t, dir, backend)
		flow := &flowRecorder{}
		if code := Run(ctx, []string{"pair", "--server", "http://localhost"}, env, flow); code != 130 {
			t.Fatalf("Run() = %d, want 130", code)
		}
		want := "Pairing cancelled before it was confirmed.\n  To finish, run:  tervi pair --server http://localhost\n"
		if stdout.String() != want || stderr.Len() != 0 || flow.startCalls+flow.finishCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
	})
	t.Run("cancelled during store read without a resumable entry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelGetBackend{MemoryBackend: credential.NewMemoryBackend(), cancel: cancel}
		env, stdout, stderr := runEnv(t, t.TempDir(), backend)
		flow := &flowRecorder{}
		if code := Run(ctx, []string{"pair", "--server", "http://localhost"}, env, flow); code != 130 {
			t.Fatalf("Run() = %d, want 130", code)
		}
		if stdout.String() != "Pairing cancelled. Nothing was saved.\n" || stderr.Len() != 0 || flow.startCalls+flow.finishCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
	})
	t.Run("cancelled during secret store check", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelSetBackend{MemoryBackend: credential.NewMemoryBackend(), cancel: cancel}
		env, stdout, stderr := runEnv(t, t.TempDir(), backend)
		flow := &flowRecorder{}
		if code := Run(ctx, []string{"pair", "--server", "http://localhost"}, env, flow); code != 130 {
			t.Fatalf("Run() = %d, want 130", code)
		}
		if stdout.String() != "Pairing cancelled. Nothing was saved.\n" || stderr.Len() != 0 || flow.startCalls+flow.finishCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
		if backend.gets != 1 || backend.deletes != 0 {
			t.Errorf("secret store operations after cancellation: gets=%d deletes=%d", backend.gets, backend.deletes)
		}
	})
}

type cancelGetBackend struct {
	*credential.MemoryBackend
	cancel context.CancelFunc
}

func (b *cancelGetBackend) Get(user string) (string, error) {
	value, err := b.MemoryBackend.Get(user)
	b.cancel()
	return value, err
}

type cancelSetBackend struct {
	*credential.MemoryBackend
	cancel  context.CancelFunc
	gets    int
	deletes int
}

func (b *cancelSetBackend) Set(user, value string) error {
	if err := b.MemoryBackend.Set(user, value); err != nil {
		return err
	}
	b.cancel()
	return nil
}

func (b *cancelSetBackend) Get(user string) (string, error) {
	b.gets++
	return b.MemoryBackend.Get(user)
}

func (b *cancelSetBackend) Delete(user string) error {
	b.deletes++
	return b.MemoryBackend.Delete(user)
}

func TestDamagedEntryWins(t *testing.T) {
	states := []*state.State{nil, {}, {MachineID: "m"}, {MachineID: "m", CredentialSaved: true}, {MachineID: "m", CredentialSaved: true, Confirmed: true}}
	for i, record := range states {
		t.Run(fmt.Sprintf("state_%d", i), func(t *testing.T) {
			dir := t.TempDir()
			if record != nil {
				if err := state.WriteState(dir, *record); err != nil {
					t.Fatal(err)
				}
			}
			backend := credential.NewMemoryBackend()
			_ = backend.Set("worker", "damaged json")
			env, stdout, stderr := runEnv(t, dir, backend)
			flow := &flowRecorder{}
			if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 0 {
				t.Fatalf("Run() = %d, want 0", code)
			}
			want := "Found a damaged secret store entry for tervi.\n  Removed the leftover pairing data. Starting a new pairing.\n"
			if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 1 || flow.finishCalls != 0 {
				t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
			}
			if _, ok := backend.Value("worker"); ok {
				t.Error("damaged entry was not removed")
			}
			if _, exists, err := state.ReadState(dir); err != nil || exists {
				t.Errorf("state remains after damaged cleanup: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestLeftoverThenStartNew(t *testing.T) {
	cases := []struct {
		name  string
		state *state.State
		entry bool
		msg   string
	}{
		{"A", nil, true, "Found incomplete pairing data: the secret store entry exists, but state.json is missing."},
		{"B", &state.State{MachineID: "m", CredentialSaved: true}, false, "Found incomplete pairing data: state.json exists, but the secret store entry is missing."},
		{"D", &state.State{MachineID: "m"}, false, "The earlier pairing was interrupted before the credential was saved."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			backend := credential.NewMemoryBackend()
			if tc.state != nil {
				_ = state.WriteState(dir, *tc.state)
			}
			if tc.entry {
				_ = backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`)
			}
			env, stdout, stderr := runEnv(t, dir, backend)
			flow := &flowRecorder{}
			if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 0 {
				t.Fatalf("Run() = %d, want 0", code)
			}
			want := tc.msg + "\n  Removed the leftover pairing data. Starting a new pairing.\n"
			if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 1 {
				t.Errorf("stdout=%q stderr=%q flow=%+v, want %q and StartNew", stdout.String(), stderr.String(), flow, want)
			}
			if _, ok := backend.Value("worker"); ok {
				t.Error("leftover entry not removed")
			}
			if _, exists, err := state.ReadState(dir); err != nil || exists {
				t.Errorf("leftover state remains: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestLeftoverDeleteFails(t *testing.T) {
	t.Run("secret store delete", func(t *testing.T) {
		dir := t.TempDir()
		_ = state.WriteState(dir, state.State{MachineID: "m"})
		backend := credential.NewMemoryBackend()
		_ = backend.Set("worker", "damaged json")
		backend.FailDelete = true
		env, stdout, stderr := runEnv(t, dir, backend)
		flow := &flowRecorder{}
		if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		want := "✗ Can't delete the secret store entry.\n  Make sure you are logged in to a desktop session and the keyring is unlocked.\n"
		if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
		if _, exists, err := state.ReadState(dir); err != nil || !exists {
			t.Errorf("state should remain after failed entry delete: exists=%v err=%v", exists, err)
		}
	})
	t.Run("state delete", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("permission checks are bypassed for the root user")
		}
		dir := t.TempDir()
		_ = state.WriteState(dir, state.State{MachineID: "m", CredentialSaved: true})
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		backend := credential.NewMemoryBackend()
		env, stdout, stderr := runEnv(t, dir, backend)
		flow := &flowRecorder{}
		if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		want := fmt.Sprintf("✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", state.StatePath(dir), dir)
		if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
	})
}

func TestCredentialNeverPrinted(t *testing.T) {
	dir := t.TempDir()
	_ = state.WriteState(dir, state.State{MachineID: "m", CredentialSaved: true})
	backend := credential.NewMemoryBackend()
	_ = backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`)
	env, stdout, stderr := runEnv(t, dir, backend)
	flow := &flowRecorder{}
	Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow)
	assertNoCredential(t, stdout.String(), stderr.String())
	if flow.finishCalls != 1 || flow.entry.Credential.Reveal() != testCredential {
		t.Fatalf("worker did not receive credential privately: %+v", flow)
	}
}

func runEnv(t *testing.T, dir string, backend credential.Backend) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Env{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Store: credential.NewStore(backend), StateDir: dir}, &stdout, &stderr
}

func assertNoCredential(t *testing.T, streams ...string) {
	t.Helper()
	for _, stream := range streams {
		if strings.Contains(stream, testCredential) {
			t.Error("output exposed credential")
		}
	}
}

var _ io.Reader = (*trapReader)(nil)
