package pair

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/worker/credential"
)

const (
	testOSMachineID      = "0123456789abcdef0123456789abcdef"
	testIdentityHash     = "7c41257dd4e161a5c7660161fed18f5296d280ea197a6a4b7a40b93180700313"
	testPlainMachineHash = "223e0a160af9da0a03e6dd2c4719c56f5d66a633cbe84e78aaa9f3735865522a"
)

func TestIdentityKnownValue(t *testing.T) {
	path := writeMachineID(t, testOSMachineID)
	got, ok := machineIdentity(path)
	if !ok || got != testIdentityHash {
		t.Fatalf("machine identity present=%t, want present with the fixed identity", ok)
	}
	if got == testPlainMachineHash {
		t.Fatal("machine identity is the plain SHA-256 of the machine ID")
	}
}

func TestIdentitySameEachTime(t *testing.T) {
	firstPath := writeMachineID(t, testOSMachineID)
	secondPath := writeMachineID(t, "fedcba9876543210fedcba9876543210")
	first, firstOK := machineIdentity(firstPath)
	repeated, repeatedOK := machineIdentity(firstPath)
	second, secondOK := machineIdentity(secondPath)
	if !firstOK || !repeatedOK || !secondOK || first != repeated || first == second {
		t.Fatal("identities are not stable per machine ID and distinct between machine IDs")
	}
}

func TestIdentityParsing(t *testing.T) {
	valid := []struct {
		name    string
		content string
	}{
		{name: "uppercase", content: strings.ToUpper(testOSMachineID)},
		{name: "trailing newline", content: testOSMachineID + "\n"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := machineIdentity(writeMachineID(t, tc.content))
			if !ok || got != testIdentityHash {
				t.Fatal("valid machine ID was not accepted as the expected identity")
			}
		})
	}
	invalid := []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "uninitialized", content: "uninitialized"},
		{name: "31 characters", content: testOSMachineID[:31]},
		{name: "33 characters", content: testOSMachineID + "0"},
		{name: "non-hex", content: testOSMachineID[:31] + "g"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := machineIdentity(writeMachineID(t, tc.content)); ok || got != "" {
				t.Fatal("invalid machine ID produced an identity")
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		if got, ok := machineIdentity(filepath.Join(t.TempDir(), "missing")); ok || got != "" {
			t.Fatal("missing machine ID produced an identity")
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		if got, ok := machineIdentity(t.TempDir()); ok || got != "" {
			t.Fatal("unreadable machine ID produced an identity")
		}
	})
}

func TestStartSendsIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "machine ID present", path: writeMachineID(t, testOSMachineID), want: testIdentityHash},
		{name: "machine ID absent", path: filepath.Join(t.TempDir(), "missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]string
			server := startThenRejectServer(t, func(r *http.Request) {
				if r.URL.Path == startPath {
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Errorf("decode start request: %v", err)
					}
				}
			})
			defer server.Close()
			var stdout bytes.Buffer
			flow := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, MachineIDPath: tc.path})
			if code := flow.StartNew(context.Background(), testEnv(t, &stdout), server.URL); code != 1 {
				t.Fatalf("StartNew() = %d, want 1", code)
			}
			if tc.want == "" {
				if _, exists := got["identity_hash"]; exists {
					t.Fatal("identity_hash was sent without a machine ID")
				}
			} else if got["identity_hash"] != tc.want {
				t.Fatal("start request did not carry the expected identity_hash")
			}
		})
	}
}

func TestIdentityNeverShown(t *testing.T) {
	machineIDDir := t.TempDir()
	machineIDPath := filepath.Join(machineIDDir, "machine-id")
	if err := os.WriteFile(machineIDPath, []byte(testOSMachineID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	backend := credentialStoreForIdentityTest()
	var startIdentity string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case startPath:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode start request: %v", err)
			}
			startIdentity = body["identity_hash"]
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
		case pollPath:
			writePoll(w, "waiting_for_code", 600, intPointer(5))
		case codePath:
			writeAccepted(w)
		case acknowledgmentPath:
			writeResult(w, map[string]string{"result": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	env := testEnv(t, &stdout)
	env.StateDir = stateDir
	env.Store = backend
	env.Stdin = strings.NewReader(testCode + "\n")
	flow := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, MachineIDPath: machineIDPath})
	if code := flow.StartNew(context.Background(), env, server.URL); code != 0 {
		t.Fatalf("StartNew() = %d, want 0", code)
	}
	if startIdentity != testIdentityHash {
		t.Fatal("start request did not send the derived identity")
	}
	if strings.Contains(stdout.String(), testOSMachineID) || strings.Contains(stdout.String(), testIdentityHash) {
		t.Fatal("machine ID or identity appeared in worker output")
	}
	files, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal("worker did not write the pairing state")
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(stateDir, file.Name()))
		if err != nil {
			t.Fatal("could not inspect a worker-written file")
		}
		if bytes.Contains(data, []byte(testOSMachineID)) || bytes.Contains(data, []byte(testIdentityHash)) {
			t.Fatal("machine ID or identity appeared in a worker-written file")
		}
	}
	if stored, exists, err := env.Store.Get(); err != nil || !exists {
		t.Fatal("worker did not save the pairing credential")
	} else if strings.Contains(stored.Credential.Reveal(), testOSMachineID) || strings.Contains(stored.Credential.Reveal(), testIdentityHash) {
		t.Fatal("machine ID or identity appeared in the stored credential")
	}
}

func writeMachineID(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func credentialStoreForIdentityTest() *credential.Store {
	return credential.NewStore(credential.NewMemoryBackend())
}
