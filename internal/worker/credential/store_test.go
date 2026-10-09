package credential

import (
	"errors"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
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
