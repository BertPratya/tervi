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

func TestFileBackendSavesPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config", "tervi")
	store := NewStore(NewFileBackend(dir))
	entry := Entry{Server: "http://localhost", Credential: secret.FromString("file-credential")}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Errorf("state folder mode = %04o, want 0700", got)
	}

	path := Path(dir)
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0600 {
		t.Errorf("credential file mode = %04o, want 0600", got)
	}
	got, exists, err := store.Get()
	if err != nil || !exists || got.Server != entry.Server || got.Credential.Reveal() != entry.Credential.Reveal() {
		t.Fatalf("Get() = (%+v, %v, %v), want saved entry", got, exists, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "credential.json" {
		t.Errorf("files after save = %v, want only credential.json", fileNames(entries))
	}
	if err := store.CheckContext(t.Context()); err != nil {
		t.Fatalf("CheckContext() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credential-check.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credential-check.json remains after CheckContext(): %v", err)
	}
}

func TestFileBackendMissingIsNotFound(t *testing.T) {
	dir := t.TempDir()
	backend := NewFileBackend(dir)
	if _, err := backend.Get("worker"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want ErrNotFound", err)
	}
	if err := backend.Delete("worker"); err != nil {
		t.Errorf("Delete(missing) error = %v, want nil", err)
	}
}

func TestFileBackendRefusesOpenPermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0640, 0604, 0660, 0644} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			backend := NewFileBackend(dir)
			if err := backend.Set("worker", `{"server":"http://localhost","credential":"value"}`); err != nil {
				t.Fatal(err)
			}
			path := Path(dir)
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Get("worker"); !errors.Is(err, ErrUnprotected) {
				t.Errorf("Get() error = %v, want ErrUnprotected", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != mode {
				t.Errorf("Get() changed mode to %04o, want unchanged %04o", got, mode)
			}
		})
	}
}

func TestFileBackendRefusesOtherOwner(t *testing.T) {
	dir := t.TempDir()
	backend := NewFileBackend(dir)
	if err := backend.Set("worker", `{"server":"http://localhost","credential":"value"}`); err != nil {
		t.Fatal(err)
	}
	otherUID := os.Getuid() + 1
	if otherUID == os.Getuid() {
		otherUID++
	}
	if _, err := newFileBackend(dir, otherUID).Get("worker"); !errors.Is(err, ErrUnprotected) {
		t.Errorf("Get() error = %v, want ErrUnprotected", err)
	}
}

func TestPairedPreservesUnprotectedCredentialError(t *testing.T) {
	dir := t.TempDir()
	if err := state.WriteState(dir, state.State{MachineID: "machine", CredentialSaved: true, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(NewFileBackend(dir))
	if err := store.Save(Entry{Server: "http://localhost", Credential: secret.FromString("value")}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(Path(dir), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, paired, err := Paired(dir, store)
	if paired || !errors.Is(err, ErrUnprotected) || errors.Is(err, ErrStoreUnreadable) {
		t.Errorf("Paired() = (paired %v, err %v), want only ErrUnprotected", paired, err)
	}
}

func fileNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func TestFileBackendNonRegularFileIsReadFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(Path(dir), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileBackend(dir).Get("worker")
	if err == nil || errors.Is(err, ErrUnprotected) || strings.Contains(err.Error(), "private") {
		t.Errorf("Get(directory) error = %v, want an ordinary read error", err)
	}
}
