package secret_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/secret"
)

func TestSecretNeverPrinted(t *testing.T) {
	value := secret.FromString("sensitive-test-value")
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		if got := fmt.Sprintf(verb, value); got != "[hidden]" {
			t.Errorf("fmt.Sprintf(%q, value) = %q, want [hidden]", verb, got)
		}
	}

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	logger.Info("event", "secret", value)
	if got := logOutput.String(); !strings.Contains(got, "[hidden]") || strings.Contains(got, value.Reveal()) {
		t.Errorf("slog output = %q, want hidden value and no secret", got)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(value) error = %v", err)
	}
	if string(encoded) != `"[hidden]"` {
		t.Errorf("json.Marshal(value) = %s, want %q", encoded, `"[hidden]"`)
	}
	if value.Reveal() != "sensitive-test-value" {
		t.Fatal("Reveal() did not return the original value")
	}
}

func TestSecretNestedNeverPrinted(t *testing.T) {
	type nested struct{ value secret.Value }
	wrapped := nested{value: secret.FromString("nested-secret-value")}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		got := fmt.Sprintf(verb, wrapped)
		if strings.Contains(got, "nested-secret-value") {
			t.Errorf("fmt.Sprintf(%q, wrapped) exposed secret: %q", verb, got)
		}
	}
}

func TestNewKey(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		value, err := secret.NewKey()
		if err != nil {
			t.Fatalf("NewKey() error = %v", err)
		}
		key := value.Reveal()
		if len(key) != 43 {
			t.Fatalf("key length = %d, want 43", len(key))
		}
		if _, err := base64.RawURLEncoding.DecodeString(key); err != nil {
			t.Fatalf("key %q is not base64url without padding: %v", key, err)
		}
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate key generated at iteration %d", i)
		}
		seen[key] = struct{}{}
	}
}

func TestNewPairingCode(t *testing.T) {
	pattern := regexp.MustCompile(`^\d{4}-\d{4}$`)
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		value, err := secret.NewPairingCode()
		if err != nil {
			t.Fatalf("NewPairingCode() error = %v", err)
		}
		code := value.Reveal()
		if !pattern.MatchString(code) {
			t.Fatalf("code = %q, want NNNN-NNNN", code)
		}
		seen[code] = struct{}{}
	}
	if len(seen) == 1 {
		t.Fatal("all generated pairing codes were equal")
	}
}

func TestNormalizeCode(t *testing.T) {
	for _, input := range []string{"4827-1934", "4827 1934", "48271934"} {
		if got := secret.NormalizeCode(input); got != "48271934" {
			t.Errorf("NormalizeCode(%q) = %q, want %q", input, got, "48271934")
		}
	}
}

func TestHash(t *testing.T) {
	value := secret.FromString("approval-key")
	want := sha256.Sum256([]byte(value.Reveal()))
	if got := secret.Hash(value); !bytes.Equal(got, want[:]) {
		t.Errorf("Hash(value) = %x, want %x", got, want)
	}
}
