package pair

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/daemon"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const revokedReplacementCredential = "replacement-credential-not-for-display"

func TestConfirmRevoked(t *testing.T) {
	t.Run("FinishEarlier signals the CLI", func(t *testing.T) {
		var stdout bytes.Buffer
		env := finishEnv(t, &stdout, strings.NewReader(""))
		entry := credential.Entry{Server: "", Credential: secret.FromString(testCredential)}
		server := revokedAckServer(t, func(r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+testCredential {
				t.Error("earlier credential was not used for the acknowledgment")
			}
		})
		defer server.Close()
		entry.Server = server.URL
		if err := state.WriteState(env.StateDir, state.State{MachineID: testMachineID, CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		flow := newFlow(Options{RequestTimeout: time.Second, MachineIDPath: filepath.Join(t.TempDir(), "missing")})
		if got := flow.FinishEarlier(context.Background(), env, entry); got != cli.StartNewPairing {
			t.Fatalf("FinishEarlier() = %d, want StartNewPairing", got)
		}
		if stdout.String() != "Finishing the earlier pairing with "+server.URL+"...\nConfirming with the server...\n" {
			t.Errorf("unexpected output before CLI handles revoked acknowledgment: %q", safeOutput(stdout.String()))
		}
		if _, exists, err := state.ReadState(env.StateDir); err != nil || !exists {
			t.Errorf("FinishEarlier changed saved state: exists=%t err=%v", exists, err)
		}
	})

	t.Run("new pairing uses its credential for handover", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
		stateDir := filepath.Join(t.TempDir(), "state")
		backend := credential.NewMemoryBackend()
		env := cli.Env{
			Stdin: strings.NewReader("y\n" + testCode + "\n"), Store: credential.NewStore(backend), StateDir: stateDir,
			ProgramPath: "/usr/bin/true", Runner: revokedServiceRunner{}, ServiceDir: t.TempDir(),
			ControlClient: revokedControlClient{newCredential: revokedReplacementCredential},
		}
		var stdout, stderr bytes.Buffer
		env.Stdout, env.Stderr = &stdout, &stderr
		if err := state.WriteState(stateDir, state.State{MachineID: testMachineID, CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		if err := env.Store.Save(credential.Entry{Server: "http://localhost", Credential: secret.FromString(testCredential)}); err != nil {
			t.Fatal(err)
		}
		var starts int
		var startSawOldState, startSawOldCredential bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case acknowledgmentPath:
				if r.Header.Get("Authorization") == "Bearer "+testCredential {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"error":"credential_revoked"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer "+revokedReplacementCredential {
					t.Error("handover acknowledgment did not use the newly issued credential")
				}
				writeResult(w, map[string]string{"result": "ok"})
			case startPath:
				starts++
				_, err := os.Stat(state.StatePath(stateDir))
				startSawOldState = err == nil
				if err != nil && !os.IsNotExist(err) {
					t.Errorf("inspect old state at new pairing start: %v", err)
				}
				_, startSawOldCredential, err = env.Store.Get()
				if err != nil {
					t.Errorf("inspect old credential at new pairing start: %v", err)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode start request: %v", err)
				}
				if _, exists := body["identity_hash"]; exists {
					t.Error("new pairing included an identity despite missing machine ID")
				}
				writeStart(w, testPollingKey, "http://example.test/pair/approval", 600)
			case pollPath:
				writePoll(w, "waiting_for_code", 600, intPointer(5))
			case codePath:
				writeResult(w, map[string]string{"result": "accepted", "credential": revokedReplacementCredential, "machine_id": "new-server-machine"})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		old, exists, err := env.Store.Get()
		if err != nil || !exists {
			t.Fatalf("read old credential: exists=%t err=%v", exists, err)
		}
		old.Server = server.URL
		if err := env.Store.Save(old); err != nil {
			t.Fatal(err)
		}
		flow := newFlow(Options{RequestTimeout: time.Second, PollInterval: time.Millisecond, MachineIDPath: filepath.Join(t.TempDir(), "missing")})
		code := cli.Run(context.Background(), []string{"pair", "--server", server.URL}, env, flow)
		wantPrefix := "Finishing the earlier pairing with " + server.URL + "...\nConfirming with the server...\n" + machineIDQuestionForPairTest
		if code != 0 || starts != 1 || startSawOldState || startSawOldCredential || stderr.Len() != 0 {
			t.Errorf("Run()=%d starts=%d old state=%t old credential=%t stderr=%q", code, starts, startSawOldState, startSawOldCredential, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), wantPrefix) || !strings.Contains(stdout.String(), "This computer was unpaired on the server. Starting a new pairing.\n") {
			t.Errorf("revoked pairing output missing expected messages: %q", safeOutput(stdout.String()))
		}
		if strings.Contains(stdout.String(), revokedReplacementCredential) || !strings.Contains(stdout.String(), "Paired, but not connected yet: [redacted]. tervi keeps trying in the background.") {
			t.Errorf("handover did not redact the new credential: %q", safeOutput(stdout.String()))
		}
		entry, exists, err := env.Store.Get()
		if err != nil || !exists || entry.Credential.Reveal() != revokedReplacementCredential {
			t.Error("new pairing did not retain its newly issued credential")
		}
	})

	t.Run("declining leaves the earlier pairing intact", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		backend := credential.NewMemoryBackend()
		env := cli.Env{Stdin: strings.NewReader("n\n"), Store: credential.NewStore(backend), StateDir: stateDir, ProgramPath: "/usr/bin/true"}
		var stdout bytes.Buffer
		env.Stdout = &stdout
		if err := state.WriteState(stateDir, state.State{MachineID: testMachineID, CredentialSaved: true}); err != nil {
			t.Fatal(err)
		}
		if err := backend.Set("worker", `{"server":"http://server.example","credential":"`+testCredential+`"}`); err != nil {
			t.Fatal(err)
		}
		beforeState, _ := os.ReadFile(state.StatePath(stateDir))
		server := revokedAckServer(t, nil)
		defer server.Close()
		old, exists, err := env.Store.Get()
		if err != nil || !exists {
			t.Fatalf("read old credential: exists=%t err=%v", exists, err)
		}
		old.Server = server.URL
		if err := env.Store.Save(old); err != nil {
			t.Fatal(err)
		}
		beforeEntry, _ := backend.Value("worker")
		flow := newFlow(Options{RequestTimeout: time.Second, MachineIDPath: filepath.Join(t.TempDir(), "missing")})
		if code := cli.Run(context.Background(), []string{"pair", "--server", server.URL}, env, flow); code != 1 {
			t.Fatalf("Run() = %d, want 1", code)
		}
		afterState, stateErr := os.ReadFile(state.StatePath(stateDir))
		afterEntry, entryExists := backend.Value("worker")
		if stateErr != nil || !bytes.Equal(beforeState, afterState) || !entryExists || beforeEntry != afterEntry {
			t.Error("declining the machine ID question changed the earlier pairing")
		}
		if !strings.HasSuffix(stdout.String(), "Nothing was changed.\n") {
			t.Errorf("decline output = %q", safeOutput(stdout.String()))
		}
	})
}

const machineIDQuestionForPairTest = "! tervi can't read this computer's machine ID (/etc/machine-id).\n" +
	"  Without it, the server won't recognize this computer if you pair it\n" +
	"  again later: it will come back as a new computer, without its history.\n" +
	"  To fix this, create a machine ID first: sudo systemd-machine-id-setup\n" +
	"Pair without it? [y/N]\n"

func revokedAckServer(t *testing.T, observe func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != acknowledgmentPath {
			http.NotFound(w, r)
			return
		}
		if observe != nil {
			observe(r)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"credential_revoked"}`)
	}))
}

type revokedServiceRunner struct{}

func (revokedServiceRunner) Run(context.Context, string, ...string) (string, error)     { return "", nil }
func (revokedServiceRunner) Stream(context.Context, io.Writer, string, ...string) error { return nil }

type revokedControlClient struct{ newCredential string }

func (revokedControlClient) Status(context.Context) (control.StatusAnswer, error) {
	return control.StatusAnswer{State: "connected"}, nil
}
func (client revokedControlClient) Connect(context.Context) (control.ConnectAnswer, error) {
	message := client.newCredential
	return control.ConnectAnswer{Result: daemon.ResultStillTrying, LastError: &message}, nil
}
func (revokedControlClient) Disconnect(context.Context) (control.DisconnectAnswer, error) {
	return control.DisconnectAnswer{}, fmt.Errorf("unexpected disconnect")
}

var _ cli.ControlClient = revokedControlClient{}
