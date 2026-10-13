package credential

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const testCredential = "credential-must-never-be-printed"

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

func TestPaired(t *testing.T) {
	t.Run("paired", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{MachineID: "m", CredentialSaved: true, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		store := NewStore(NewMemoryBackend())
		if err := store.Save(Entry{Server: "http://localhost", Credential: secret.FromString(testCredential)}); err != nil {
			t.Fatal(err)
		}
		entry, record, paired, err := Paired(dir, store)
		if err != nil || !paired || entry.Server != "http://localhost" || entry.Credential.Reveal() != testCredential || !record.Confirmed {
			t.Fatalf("Paired() = (%+v, %+v, %v, %v), want paired", entry, record, paired, err)
		}
	})

	t.Run("missing state", func(t *testing.T) {
		_, _, paired, err := Paired(t.TempDir(), NewStore(NewMemoryBackend()))
		if paired || err != nil {
			t.Fatalf("Paired() = (%v, %v), want not paired without error", paired, err)
		}
	})

	t.Run("not confirmed", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		_, _, paired, err := Paired(dir, NewStore(NewMemoryBackend()))
		if paired || err != nil {
			t.Fatalf("Paired() = (%v, %v), want not paired without error", paired, err)
		}
	})

	t.Run("missing entry", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		_, _, paired, err := Paired(dir, NewStore(NewMemoryBackend()))
		if paired || err != nil {
			t.Fatalf("Paired() = (%v, %v), want not paired without error", paired, err)
		}
	})

	t.Run("unreadable store", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		backend := NewMemoryBackend()
		backend.FailGet = true
		_, _, paired, err := Paired(dir, NewStore(backend))
		if paired || !errors.Is(err, ErrStoreUnreadable) {
			t.Fatalf("Paired() = (%v, %v), want store error", paired, err)
		}
	})

	t.Run("damaged entry", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.WriteState(dir, state.State{Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		backend := NewMemoryBackend()
		if err := backend.Set("worker", "broken"); err != nil {
			t.Fatal(err)
		}
		_, _, paired, err := Paired(dir, NewStore(backend))
		if paired || !errors.Is(err, ErrDamaged) {
			t.Fatalf("Paired() = (%v, %v), want damaged entry error", paired, err)
		}
	})

	t.Run("unreadable state", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "state.json"), 0700); err != nil {
			t.Fatal(err)
		}
		_, _, paired, err := Paired(dir, NewStore(NewMemoryBackend()))
		if paired || !errors.Is(err, ErrStateUnreadable) {
			t.Fatalf("Paired() = (%v, %v), want state read error", paired, err)
		}
	})
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
