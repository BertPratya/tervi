package local

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

	"github.com/bertpratya/tervi/internal/secret"
)

const testCredential = "credential-must-never-be-printed"

type flowRecorder struct {
	startCalls  int
	finishCalls int
	server      string
	entry       Entry
	startCode   int
	finishCode  int
}

func (f *flowRecorder) StartNew(_ context.Context, _ Env, server string) int {
	f.startCalls++
	f.server = server
	return f.startCode
}

func (f *flowRecorder) FinishEarlier(_ context.Context, _ Env, entry Entry) int {
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
	return "", ErrNotFound
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
	store := NewStore(backend)
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
		env := Env{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Store: NewStore(NewMemoryBackend()), StateDir: filepath.Join(t.TempDir(), "state")}
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

func TestAddressValid(t *testing.T) {
	for _, address := range []string{"http://localhost:8080", "https://example.com", "http://localhost:8080/", "http://a:65535", "http://[::1]:8080"} {
		if _, err := StandardAddress(address); err != nil {
			t.Errorf("StandardAddress(%q) error = %v", address, err)
		}
	}
	for _, address := range []string{"banana", "ftp://x", "http://", "http://localhost:8080/foo", "http://a?b", "http://a#b", "http://u@a", "http://a:8080:9090", "http://[fe80::1%25eth0]:80", "http://a:0", "http://a:70000"} {
		if _, err := StandardAddress(address); err == nil {
			t.Errorf("StandardAddress(%q) succeeded, want invalid address", address)
		}
	}
}

func TestAddressStandardForm(t *testing.T) {
	for input, want := range map[string]string{
		"HTTP://LOCALHOST:8080/":    "http://localhost:8080",
		"https://example.com:443":   "https://example.com",
		"http://example.com:80":     "http://example.com",
		"HTTP://EXAMPLE.COM:08080/": "http://example.com:8080",
	} {
		got, err := StandardAddress(input)
		if err != nil || got != want {
			t.Errorf("StandardAddress(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestStoreCheck(t *testing.T) {
	t.Run("working backend", func(t *testing.T) {
		backend := NewMemoryBackend()
		if err := NewStore(backend).Check(); err != nil {
			t.Fatalf("Check() error = %v", err)
		}
		if _, ok := backend.Value("worker-check"); ok {
			t.Fatal("Check() left its test value behind")
		}
	})
	for _, failure := range []string{"get", "set", "delete", "change on read"} {
		t.Run(failure, func(t *testing.T) {
			backend := NewMemoryBackend()
			switch failure {
			case "get":
				backend.FailGet = true
			case "set":
				backend.FailSet = true
			case "delete":
				backend.FailDelete = true
			case "change on read":
				backend.ChangeOnRead = true
			}
			if err := NewStore(backend).Check(); err == nil {
				t.Fatal("Check() succeeded, want unusable store error")
			}
		})
	}
}

func TestStoreSaveVerified(t *testing.T) {
	backend := NewMemoryBackend()
	backend.ChangeOnRead = true
	if err := NewStore(backend).Save(Entry{Server: "http://localhost", Credential: secret.FromString(testCredential)}); err == nil {
		t.Fatal("Save() succeeded when read-back differed")
	}
}

func TestEntryJSONHoldsRealCredential(t *testing.T) {
	backend := NewMemoryBackend()
	if err := NewStore(backend).Save(Entry{Server: "http://localhost", Credential: secret.FromString(testCredential)}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	value, ok := backend.Value("worker")
	if !ok || !strings.Contains(value, testCredential) || strings.Contains(value, "[hidden]") {
		t.Fatalf("stored entry missing revealed credential or contains placeholder: found=%v hidden=%v", strings.Contains(value, testCredential), strings.Contains(value, "[hidden]"))
	}
}

func TestEntryWellFormed(t *testing.T) {
	for _, raw := range []string{
		"not json",
		`{"server":"HTTP://localhost/","credential":"x"}`,
		`{"server":"http://localhost","credential":""}`,
	} {
		t.Run(raw, func(t *testing.T) {
			backend := NewMemoryBackend()
			if err := backend.Set("worker", raw); err != nil {
				t.Fatal(err)
			}
			_, exists, err := NewStore(backend).Get()
			if !exists || !errors.Is(err, ErrDamaged) {
				t.Fatalf("Get() = (_, %v, %v), want existing damaged entry", exists, err)
			}
		})
	}
}

func TestStateFileWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	want := State{MachineID: "machine-123", CredentialSaved: true, Confirmed: false}
	if err := WriteState(dir, want); err != nil {
		t.Fatalf("WriteState() error = %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Errorf("directory mode = %04o, want 0700", got)
	}
	info, err = os.Stat(StatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("state file mode = %04o, want 0600", got)
	}
	got, exists, err := ReadState(dir)
	if err != nil || !exists || got != want {
		t.Fatalf("ReadState() = (%+v, %v, %v), want (%+v, true, nil)", got, exists, err, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory contains partial/temp files: %v", entries)
	}
	data, err := os.ReadFile(StatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded) != 3 {
		t.Errorf("state JSON = %q, decode err %v; want all three fields only", data, err)
	}
}

func TestStateFileBroken(t *testing.T) {
	dir := t.TempDir()
	path := StatePath(dir)
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	backend := NewMemoryBackend()
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
		state           *State
		entry           string
		wantOut         string
		wantCode        int
		start           int
		finish          int
		wantState       *State
		wantStateExists bool
		wantStateSame   bool
		wantEntry       string
		wantEntryExists bool
	}{
		{name: "missing both", wantCode: 0, start: 1},
		{name: "missing state", entry: entryRaw(server), wantOut: "Found incomplete pairing data: the secret store entry exists, but state.json is missing.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "saved false but entry missing", state: &State{MachineID: "m"}, wantOut: "The earlier pairing was interrupted before the credential was saved.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "saved false and entry exists", state: &State{MachineID: "m"}, entry: entryRaw(server), wantCode: 0, finish: 1, wantState: &State{MachineID: "m", CredentialSaved: true}, wantStateExists: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "saved true and entry exists", state: &State{MachineID: "m", CredentialSaved: true}, entry: entryRaw(server), wantCode: 0, finish: 1, wantState: &State{MachineID: "m", CredentialSaved: true}, wantStateExists: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "saved true but entry missing", state: &State{MachineID: "m", CredentialSaved: true}, wantOut: "Found incomplete pairing data: state.json exists, but the secret store entry is missing.\n" + baseMessage, wantCode: 0, start: 1},
		{name: "confirmed", state: &State{MachineID: "m", CredentialSaved: true, Confirmed: true}, entry: entryRaw(server), wantOut: "This computer is already paired with http://localhost. Nothing was changed.\n", wantCode: 1, wantState: &State{MachineID: "m", CredentialSaved: true, Confirmed: true}, wantStateExists: true, wantStateSame: true, wantEntry: entryRaw(server), wantEntryExists: true},
		{name: "damaged entry", state: &State{MachineID: "m"}, entry: "damaged json", wantOut: "Found a damaged secret store entry for tervi.\n" + baseMessage, wantCode: 0, start: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			backend := NewMemoryBackend()
			if tc.state != nil {
				if err := WriteState(dir, *tc.state); err != nil {
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
				stateBefore, err = os.ReadFile(StatePath(dir))
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
			state, stateExists, stateErr := ReadState(dir)
			if stateErr != nil {
				t.Fatalf("ReadState() error = %v", stateErr)
			}
			if stateExists != tc.wantStateExists {
				t.Errorf("state exists = %v, want %v", stateExists, tc.wantStateExists)
			}
			if tc.wantStateExists && tc.wantState != nil && state != *tc.wantState {
				t.Errorf("state after run = %+v, want %+v", state, *tc.wantState)
			}
			if tc.wantStateSame {
				stateAfter, err := os.ReadFile(StatePath(dir))
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
			state := State{MachineID: "m", CredentialSaved: saved}
			if err := WriteState(dir, state); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(StatePath(dir))
			backend := NewMemoryBackend()
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
			after, _ := os.ReadFile(StatePath(dir))
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
	backend := NewMemoryBackend()
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
	backend := NewMemoryBackend()
	if err := backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "read-only")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(dir, State{MachineID: "m", CredentialSaved: false}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	env, stdout, stderr := runEnv(t, dir, backend)
	flow := &flowRecorder{}
	code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow)
	if code != 1 || stdout.String() != fmt.Sprintf("✗ Can't write %s.\n", StatePath(dir)) || stderr.Len() != 0 || flow.finishCalls != 0 {
		t.Errorf("code=%d stdout=%q stderr=%q flow=%+v", code, stdout.String(), stderr.String(), flow)
	}
}

func TestCtrlCDuringStepZero(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := NewMemoryBackend()
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
		if err := WriteState(dir, State{MachineID: "m", CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		backend := &cancelGetBackend{MemoryBackend: NewMemoryBackend(), cancel: cancel}
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
		backend := &cancelGetBackend{MemoryBackend: NewMemoryBackend(), cancel: cancel}
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
		backend := &cancelSetBackend{MemoryBackend: NewMemoryBackend(), cancel: cancel}
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
	*MemoryBackend
	cancel context.CancelFunc
}

func (b *cancelGetBackend) Get(user string) (string, error) {
	value, err := b.MemoryBackend.Get(user)
	b.cancel()
	return value, err
}

type cancelSetBackend struct {
	*MemoryBackend
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
	states := []*State{nil, {}, {MachineID: "m"}, {MachineID: "m", CredentialSaved: true}, {MachineID: "m", CredentialSaved: true, Confirmed: true}}
	for i, state := range states {
		t.Run(fmt.Sprintf("state_%d", i), func(t *testing.T) {
			dir := t.TempDir()
			if state != nil {
				if err := WriteState(dir, *state); err != nil {
					t.Fatal(err)
				}
			}
			backend := NewMemoryBackend()
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
			if _, exists, err := ReadState(dir); err != nil || exists {
				t.Errorf("state remains after damaged cleanup: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestLeftoverThenStartNew(t *testing.T) {
	cases := []struct {
		name  string
		state *State
		entry bool
		msg   string
	}{
		{"A", nil, true, "Found incomplete pairing data: the secret store entry exists, but state.json is missing."},
		{"B", &State{MachineID: "m", CredentialSaved: true}, false, "Found incomplete pairing data: state.json exists, but the secret store entry is missing."},
		{"D", &State{MachineID: "m"}, false, "The earlier pairing was interrupted before the credential was saved."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			backend := NewMemoryBackend()
			if tc.state != nil {
				_ = WriteState(dir, *tc.state)
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
			if _, exists, err := ReadState(dir); err != nil || exists {
				t.Errorf("leftover state remains: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestLeftoverDeleteFails(t *testing.T) {
	t.Run("secret store delete", func(t *testing.T) {
		dir := t.TempDir()
		_ = WriteState(dir, State{MachineID: "m"})
		backend := NewMemoryBackend()
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
		if _, exists, err := ReadState(dir); err != nil || !exists {
			t.Errorf("state should remain after failed entry delete: exists=%v err=%v", exists, err)
		}
	})
	t.Run("state delete", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("permission checks are bypassed for the root user")
		}
		dir := t.TempDir()
		_ = WriteState(dir, State{MachineID: "m", CredentialSaved: true})
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		backend := NewMemoryBackend()
		env, stdout, stderr := runEnv(t, dir, backend)
		flow := &flowRecorder{}
		if code := Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		want := fmt.Sprintf("✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", StatePath(dir), dir)
		if stdout.String() != want || stderr.Len() != 0 || flow.startCalls != 0 {
			t.Errorf("stdout=%q stderr=%q flow=%+v", stdout.String(), stderr.String(), flow)
		}
	})
}

func TestStateFileDelete(t *testing.T) {
	dir := t.TempDir()
	if err := WriteState(dir, State{MachineID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteState(dir); err != nil {
		t.Fatalf("DeleteState() error = %v", err)
	}
	if err := DeleteState(dir); err != nil {
		t.Fatalf("DeleteState(missing) error = %v", err)
	}
	if _, err := os.Stat(StatePath(dir)); !os.IsNotExist(err) {
		t.Errorf("state still exists or stat error: %v", err)
	}
}

func TestCredentialNeverPrinted(t *testing.T) {
	dir := t.TempDir()
	_ = WriteState(dir, State{MachineID: "m", CredentialSaved: true})
	backend := NewMemoryBackend()
	_ = backend.Set("worker", `{"server":"http://localhost","credential":"`+testCredential+`"}`)
	env, stdout, stderr := runEnv(t, dir, backend)
	flow := &flowRecorder{}
	Run(context.Background(), []string{"pair", "--server", "http://localhost"}, env, flow)
	assertNoCredential(t, stdout.String(), stderr.String())
	if flow.finishCalls != 1 || flow.entry.Credential.Reveal() != testCredential {
		t.Fatalf("worker did not receive credential privately: %+v", flow)
	}
}

func runEnv(t *testing.T, dir string, backend Backend) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Env{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Store: NewStore(backend), StateDir: dir}, &stdout, &stderr
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
