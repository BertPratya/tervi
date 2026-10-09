package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
		"http://[::1]:8080":         "http://[::1]:8080",
	} {
		got, err := StandardAddress(input)
		if err != nil || got != want {
			t.Errorf("StandardAddress(%q) = %q, %v; want %q", input, got, err, want)
		}
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
