package flow

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/local"
)

const (
	testCode       = "8765-4321"
	testCredential = "credential-must-not-be-printed"
	testMachineID  = "machine-id-test"
)

func TestPromptShowsSameExpiry(t *testing.T) {
	base := time.Date(2026, time.January, 2, 14, 22, 0, 0, time.Local)
	server := codeServerWithExpiryAndTries(t, 300, 3, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()

	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
	code := finishFlow(base, time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
	if code != 0 {
		t.Fatalf("StartNew() = %d, want 0; output %q", code, safeOutput(out.String()))
	}
	var startExpiry string
	for _, line := range strings.Split(out.String(), "\n") {
		const prefix = "Waiting for approval... (expires at "
		if strings.HasPrefix(line, prefix) {
			startExpiry = strings.TrimSuffix(strings.TrimPrefix(line, prefix), ")")
		}
	}
	want := fmt.Sprintf("Type the code shown in the browser (expires at %s, 3 tries left):\nCode: ", startExpiry)
	if startExpiry == "" || startExpiry != base.Add(10*time.Minute).Local().Format("15:04") || !strings.Contains(out.String(), want) {
		t.Errorf("prompt does not repeat start expiry %q with server tries: %q", startExpiry, safeOutput(out.String()))
	}
}

func TestCodeReplyMayArriveAfterDeadline(t *testing.T) {
	server := codeServerWithExpiry(t, 1, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		if r.URL.Path == "/api/v1/pairings/current/code" {
			time.Sleep(450 * time.Millisecond)
			writeAccepted(w)
			return
		}
		http.NotFound(w, r)
	})
	defer server.Close()

	var out bytes.Buffer
	env := finishEnv(t, &out, &delayedReader{reader: bytes.NewReader([]byte(testCode + "\n")), delay: 650 * time.Millisecond})
	flow := newFlow(Options{RequestTimeout: 800 * time.Millisecond, PollInterval: time.Millisecond})
	got := flow.StartNew(context.Background(), env, server.URL)
	state, exists, err := local.ReadState(env.StateDir)
	if got != 0 || !strings.Contains(out.String(), "✓ Paired successfully.\n") || err != nil || !exists || !state.Confirmed {
		t.Errorf("StartNew() = %d, state=%#v exists=%t err=%v output=%q; want accepted reply and completed save", got, state, exists, err, safeOutput(out.String()))
	}
}

func TestCodeInputEOF(t *testing.T) {
	var codeRequests atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings/current/code" {
			codeRequests.Add(1)
		}
		writeAccepted(w)
	})
	defer server.Close()
	var out bytes.Buffer
	got := finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader("")), server.URL)
	want := "✗ Input ended before a code was entered. Nothing was saved. Run the command again.\n"
	if got != 1 || codeRequests.Load() != 0 || !strings.Contains(out.String(), want) {
		t.Errorf("StartNew() = %d, code requests=%d, output=%q", got, codeRequests.Load(), safeOutput(out.String()))
	}
}

func TestPartialCodeWrongAnswerStopsAtNextPrompt(t *testing.T) {
	var codeRequests atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings/current/code" {
			codeRequests.Add(1)
			writeResult(w, map[string]any{"result": "wrong_code", "tries_left": 4})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()
	var out bytes.Buffer
	started := time.Now()
	got := finishFlow(started, time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader("0000")), server.URL)
	want := "✗ Input ended before a code was entered. Nothing was saved. Run the command again.\n"
	if got != 1 || codeRequests.Load() != 1 || !strings.Contains(out.String(), "✗ Wrong code. 4 tries left.\nCode: \n"+want) {
		t.Errorf("StartNew() = %d, code requests=%d, output=%q", got, codeRequests.Load(), safeOutput(out.String()))
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Errorf("flow waited %s after EOF; want it to stop before the code deadline", elapsed)
	}
}

func TestCodeInputReadError(t *testing.T) {
	var codeRequests atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings/current/code" {
			codeRequests.Add(1)
		}
		writeAccepted(w)
	})
	defer server.Close()
	var out bytes.Buffer
	got := finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, failingReader{}), server.URL)
	want := "✗ Couldn't read the code. Nothing was saved. Run the command again.\n"
	if got != 1 || codeRequests.Load() != 0 || !strings.Contains(out.String(), want) {
		t.Errorf("StartNew() = %d, code requests=%d, output=%q", got, codeRequests.Load(), safeOutput(out.String()))
	}
}

func TestCodeDeadline(t *testing.T) {
	server := codeServerWithExpiry(t, 1, func(w http.ResponseWriter, r *http.Request) { t.Error("code sent after deadline") })
	defer server.Close()
	var out bytes.Buffer
	reader := newBlockingReader()
	defer reader.close()
	code := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, reader), server.URL)
	if code != 1 || !strings.Contains(out.String(), "✗ The code expired. Run the command again.\n") {
		t.Errorf("StartNew() = %d, output %q; want expiry message and exit 1", code, safeOutput(out.String()))
	}
}

func TestWrongCodes(t *testing.T) {
	var submitted atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		submitted.Add(1)
		if submitted.Load() == 1 {
			writeResult(w, map[string]any{"result": "wrong_code", "tries_left": 3})
		} else {
			writeResult(w, map[string]any{"result": "failed"})
		}
	})
	defer server.Close()
	var out bytes.Buffer
	code := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader("0000\n1111\n")), server.URL)
	if code != 1 || submitted.Load() != 2 {
		t.Fatalf("StartNew() = %d, submitted %d codes; want exit 1 and 2 requests", code, submitted.Load())
	}
	got := out.String()
	for _, want := range []string{"✗ Wrong code. 3 tries left.\n", "✗ Too many wrong codes. Pairing failed. Run the command again.\n", "5 tries left):"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %q", want, safeOutput(got))
		}
	}
}

func TestCodeAnswers(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		code             int
	}{
		{name: "expired", status: http.StatusOK, body: `{"result":"expired"}`, want: "✗ The code expired. Run the command again.\n", code: 1},
		{name: "not waiting", status: http.StatusOK, body: `{"result":"not_waiting_for_code","status":"rejected"}`, want: "✗ The pairing is no longer waiting for a code. Run the command again.\n", code: 1},
		{name: "unknown key", status: http.StatusUnauthorized, body: `{"error":"unknown_key"}`, want: "✗ The server no longer knows this pairing. Run the command again.\n", code: 1},
		{name: "unexpected", status: http.StatusOK, body: `{"result":"surprise"}`, want: "✗ Unexpected answer from the server. Run the command again.\n", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := codeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			defer server.Close()
			var out bytes.Buffer
			got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader(testCode+"\n")), server.URL)
			if got != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Errorf("StartNew() = %d, output %q; want %d and %q", got, safeOutput(out.String()), tc.code, tc.want)
			}
		})
	}
}

func TestCodeNeverResent(t *testing.T) {
	for _, tc := range []struct {
		name string
		code func(http.ResponseWriter, *http.Request)
	}{
		{"no answer", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{"503", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }},
		{"invalid JSON", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"result":`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent atomic.Int32
			server := codeServer(t, func(w http.ResponseWriter, r *http.Request) { sent.Add(1); tc.code(w, r) })
			defer server.Close()
			var out bytes.Buffer
			got := finishFlow(time.Now().In(time.Local), 30*time.Millisecond, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader(testCode+"\n")), server.URL)
			if got != 1 || sent.Load() != 1 || !strings.Contains(out.String(), "✗ No answer from the server after sending the code. Run the same command again to start over.\n") {
				t.Errorf("StartNew() = %d, code requests %d, output %q", got, sent.Load(), safeOutput(out.String()))
			}
		})
	}
}

func TestCodeNeverPrinted(t *testing.T) {
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()
	var out bytes.Buffer
	finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader(testCode+"\n")), server.URL)
	if strings.Contains(out.String(), testCode) {
		t.Errorf("typed code appeared in output: %q", safeOutput(out.String()))
	}
}

func TestEmptyCodeLineDoesNotSend(t *testing.T) {
	var codeRequests atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pairings/current/code" {
			codeRequests.Add(1)
			writeAccepted(w)
			return
		}
		writeResult(w, map[string]string{"result": "ok"})
	})
	defer server.Close()
	var out bytes.Buffer
	got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader("\n"+testCode+"\n")), server.URL)
	if got != 0 || codeRequests.Load() != 1 || strings.Count(out.String(), "Code: ") != 2 {
		t.Errorf("exit=%d code requests=%d output=%q", got, codeRequests.Load(), safeOutput(out.String()))
	}
}

func TestHappyPath(t *testing.T) {
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
	backend := local.NewMemoryBackend()
	var eventMu sync.Mutex
	var events []string
	appendEvent := func(event string) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}
	env.Store = local.NewStore(&recordingBackend{backend: backend, observe: func(event string) {
		state, exists, err := local.ReadState(env.StateDir)
		if err != nil || !exists || state.MachineID != testMachineID || state.CredentialSaved || state.Confirmed {
			t.Errorf("state before secret store %s = %#v, exists %t, err %v", event, state, exists, err)
		}
		appendEvent(event)
	}})
	var ackSawSavedState atomic.Bool
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			state, exists, err := local.ReadState(env.StateDir)
			ackSawSavedState.Store(err == nil && exists && state.MachineID == testMachineID && state.CredentialSaved && !state.Confirmed)
			if r.Header.Get("Authorization") != "Bearer "+testCredential {
				t.Errorf("ack proof was not credential")
			}
			appendEvent("acknowledgment")
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()

	got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
	if got != 0 || !strings.Contains(out.String(), "✓ Paired successfully.\n") || !ackSawSavedState.Load() {
		t.Fatalf("StartNew() = %d, ack saw saved state %t, output %q", got, ackSawSavedState.Load(), safeOutput(out.String()))
	}
	state, exists, err := local.ReadState(env.StateDir)
	if err != nil || !exists || !state.CredentialSaved || !state.Confirmed || state.MachineID != testMachineID {
		t.Errorf("final state = %#v, exists %t, err %v", state, exists, err)
	}
	appendEvent("confirmed true")
	rawEntry, entryExists := backend.Value("worker")
	if !entryExists || !strings.Contains(rawEntry, `"server":"`+server.URL+`"`) || !strings.Contains(rawEntry, testCredential) {
		t.Errorf("saved entry was missing or did not contain the expected server and credential")
	}
	eventMu.Lock()
	gotEvents := append([]string(nil), events...)
	eventMu.Unlock()
	wantEvents := []string{"store set", "store read back", "acknowledgment", "confirmed true"}
	if fmt.Sprint(gotEvents) != fmt.Sprint(wantEvents) {
		t.Errorf("save and confirmation order = %v, want %v", gotEvents, wantEvents)
	}
	_ = filepath.Walk(env.StateDir, func(path string, _ os.FileInfo, err error) error {
		if err == nil {
			data, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Contains(data, []byte(testCredential)) {
				t.Errorf("credential was written under StateDir at %s", path)
			}
		}
		return nil
	})
}

func TestStateWriteFails(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(t *testing.T, env *local.Env, backend *local.MemoryBackend, cancel context.CancelFunc) func(http.ResponseWriter, *http.Request)
		want             func(env local.Env) string
		saveFailureCalls int
	}{
		{name: "initial state", setup: func(t *testing.T, env *local.Env, _ *local.MemoryBackend, _ context.CancelFunc) func(http.ResponseWriter, *http.Request) {
			if err := os.MkdirAll(env.StateDir, 0700); err != nil {
				t.Fatal(err)
			}
			makeStateDirReadOnly(t, env.StateDir)
			return func(w http.ResponseWriter, _ *http.Request) { writeAccepted(w) }
		}, want: func(env local.Env) string {
			return fmt.Sprintf("✗ Can't write %s. Pairing was not completed.\n", local.StatePath(env.StateDir))
		}, saveFailureCalls: 1},
		{name: "credential-saved state", setup: func(t *testing.T, env *local.Env, _ *local.MemoryBackend, _ context.CancelFunc) func(http.ResponseWriter, *http.Request) {
			return func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
					writeResult(w, map[string]string{"result": "ok"})
					return
				}
				writeAccepted(w)
			}
		}, want: func(env local.Env) string {
			return fmt.Sprintf("✗ Can't write %s.\n  To finish, run:  tervi pair --server ", local.StatePath(env.StateDir))
		}},
		{name: "confirmed state", setup: func(t *testing.T, env *local.Env, _ *local.MemoryBackend, _ context.CancelFunc) func(http.ResponseWriter, *http.Request) {
			return func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
					makeStateDirReadOnly(t, env.StateDir)
					writeResult(w, map[string]string{"result": "ok"})
					return
				}
				writeAccepted(w)
			}
		}, want: func(env local.Env) string {
			return fmt.Sprintf("✗ The server confirmed the pairing, but this computer couldn't record it: can't write %s.\n  To finish, run:  tervi pair --server ", local.StatePath(env.StateDir))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
			backend := local.NewMemoryBackend()
			if tc.name == "credential-saved state" {
				env.Store = local.NewStore(&sabotageBackend{MemoryBackend: backend, onSet: func() { makeStateDirReadOnly(t, env.StateDir) }})
			} else {
				env.Store = local.NewStore(backend)
			}
			var failures atomic.Int32
			handler := tc.setup(t, &env, backend, nil)
			server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/machines/current/save-failure" {
					failures.Add(1)
					writeResult(w, map[string]string{"result": "ok"})
					return
				}
				handler(w, r)
			})
			defer server.Close()
			got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
			if got != 1 || !strings.Contains(out.String(), tc.want(env)) || failures.Load() != int32(tc.saveFailureCalls) {
				t.Errorf("exit=%d failures=%d output=%q", got, failures.Load(), safeOutput(out.String()))
			}
		})
	}
}

func TestSaveFails(t *testing.T) { runSaveFailureTest(t, false) }

func TestSaveFailsAndDeleteFails(t *testing.T) { runSaveFailureTest(t, true) }

func runSaveFailureTest(t *testing.T, failDelete bool) {
	t.Helper()
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
	backend := local.NewMemoryBackend()
	backend.ChangeOnRead = true
	backend.FailDelete = failDelete
	env.Store = local.NewStore(backend)
	var reports atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/save-failure" {
			reports.Add(1)
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()
	got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
	if got != 1 || reports.Load() != 1 || !strings.Contains(out.String(), "✗ Couldn't save the credential. Pairing was not completed.\n") {
		t.Errorf("exit=%d reports=%d output=%q", got, reports.Load(), safeOutput(out.String()))
	}
	if failDelete {
		if !strings.Contains(out.String(), "✗ Couldn't remove the partly saved secret store entry.\n  Make sure you are logged in to a desktop session and the keyring is unlocked, then run the same command again.\n") {
			t.Errorf("missing partial-entry cleanup failure: %q", safeOutput(out.String()))
		}
		if _, exists, _ := local.ReadState(env.StateDir); !exists {
			t.Error("state.json removed despite secret delete failure")
		}
		if _, exists := backend.Value("worker"); !exists {
			t.Error("partly saved secret entry disappeared despite delete failure")
		}
	} else {
		if _, exists := backend.Value("worker"); exists {
			t.Error("secret entry was not deleted")
		}
		if _, exists, _ := local.ReadState(env.StateDir); exists {
			t.Error("state.json was not deleted")
		}
		if strings.Contains(out.String(), "Can't delete") {
			t.Errorf("unexpected cleanup error: %q", safeOutput(out.String()))
		}
	}
}

func TestAckRetries(t *testing.T) {
	const interval = 35 * time.Millisecond
	var mu sync.Mutex
	var times []time.Time
	var acks atomic.Int32
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/machines/current/acknowledgment" {
			writeAccepted(w)
			return
		}
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		acks.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer server.Close()
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
	got := finishFlow(time.Now().In(time.Local), time.Second, interval).StartNew(context.Background(), env, server.URL)
	if got != 1 || acks.Load() != 3 {
		t.Fatalf("exit=%d ack requests=%d; want three attempts", got, acks.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(times); i++ {
		if delay := times[i].Sub(times[i-1]); delay < interval {
			t.Errorf("ack try %d began after %s, want at least %s", i+1, delay, interval)
		}
	}
	for _, want := range []string{"Confirming with the server...", "No answer, retrying (2 of 3)...", "No answer, retrying (3 of 3)...", "✗ Saved, but couldn't confirm with the server.\n", "  To finish, run:  tervi pair --server " + server.URL} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %q", want, safeOutput(out.String()))
		}
	}
	state, exists, err := local.ReadState(env.StateDir)
	if err != nil || !exists || !state.CredentialSaved || state.Confirmed {
		t.Errorf("state after failed retries = %#v exists=%t err=%v", state, exists, err)
	}

	t.Run("request timeout waits after each try ends", func(t *testing.T) {
		const requestTimeout = 40 * time.Millisecond
		const interval = 30 * time.Millisecond
		var mu sync.Mutex
		var starts, ends []time.Time
		server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/machines/current/acknowledgment" {
				writeAccepted(w)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			<-r.Context().Done()
			mu.Lock()
			ends = append(ends, time.Now())
			mu.Unlock()
		})
		defer server.Close()
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
		got := finishFlow(time.Now(), requestTimeout, interval).StartNew(context.Background(), env, server.URL)
		if got != 1 {
			t.Fatalf("StartNew() = %d; want 1", got)
		}
		mu.Lock()
		gotStarts := append([]time.Time(nil), starts...)
		gotEnds := append([]time.Time(nil), ends...)
		mu.Unlock()
		if len(gotStarts) != 3 || len(gotEnds) != 3 {
			t.Fatalf("ack starts=%d ends=%d; want three timed-out tries", len(gotStarts), len(gotEnds))
		}
		for i := 1; i < len(gotStarts); i++ {
			if gap := gotStarts[i].Sub(gotEnds[i-1]); gap < interval {
				t.Errorf("ack try %d began %s after prior try ended, want at least %s", i+1, gap, interval)
			}
		}
	})
}

func TestAckResults(t *testing.T) {
	for _, tc := range []struct{ result, want string }{
		{"expired", "✗ The pairing didn't finish in time. Run the same command again to start a new one.\n"},
		{"failed", "✗ The pairing failed. Run the same command again to start a new one.\n"},
		{"unknown_credential", "✗ The server doesn't recognize this pairing. Run the same command again to start a new one.\n"},
	} {
		t.Run(tc.result, func(t *testing.T) {
			var out bytes.Buffer
			env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
			backend := local.NewMemoryBackend()
			env.Store = local.NewStore(backend)
			status := http.StatusOK
			result := tc.result
			if result == "unknown_credential" {
				status, result = http.StatusUnauthorized, ""
			}
			server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
					w.WriteHeader(status)
					if result != "" {
						writeResult(w, map[string]string{"result": result})
					} else {
						_, _ = io.WriteString(w, `{"error":"unknown_credential"}`)
					}
					return
				}
				writeAccepted(w)
			})
			defer server.Close()
			got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
			if got != 1 || !strings.Contains(out.String(), tc.want) {
				t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
			}
			if _, exists := backend.Value("worker"); exists {
				t.Error("entry was not removed")
			}
			if _, exists, _ := local.ReadState(env.StateDir); exists {
				t.Error("state was not removed")
			}
		})
	}
}

func TestAckCleanupDeleteFails(t *testing.T) {
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
	backend := local.NewMemoryBackend()
	backend.FailDelete = true
	env.Store = local.NewStore(backend)
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			writeResult(w, map[string]string{"result": "expired"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()
	got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
	want := "✗ Can't delete the secret store entry.\n  Make sure you are logged in to a desktop session and the keyring is unlocked.\n"
	if got != 1 || !strings.Contains(out.String(), want) {
		t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
	}
	if strings.Contains(out.String(), "✗ The pairing didn't finish in time. Run the same command again to start a new one.") {
		t.Errorf("expired result message printed despite secret delete failure: %q", safeOutput(out.String()))
	}
	if _, exists, _ := local.ReadState(env.StateDir); !exists {
		t.Error("state should be kept after secret delete failure")
	}
}

func TestStateDeleteFails(t *testing.T) {
	t.Run("after save failure", func(t *testing.T) {
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
		backend := &sabotageBackend{MemoryBackend: local.NewMemoryBackend(), onSet: func() { makeStateDirReadOnly(t, env.StateDir) }}
		backend.FailSet = true
		env.Store = local.NewStore(backend)
		server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/machines/current/save-failure" {
				writeResult(w, map[string]string{"result": "ok"})
				return
			}
			writeAccepted(w)
		})
		defer server.Close()
		got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
		if got != 1 || !strings.Contains(out.String(), deleteStateMessage(env.StateDir)) {
			t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
		}
	})
	t.Run("after expired", func(t *testing.T) {
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
		server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
				makeStateDirReadOnly(t, env.StateDir)
				writeResult(w, map[string]string{"result": "expired"})
				return
			}
			writeAccepted(w)
		})
		defer server.Close()
		got := finishFlow(time.Now().In(time.Local), time.Second, time.Millisecond).StartNew(context.Background(), env, server.URL)
		if got != 1 || !strings.Contains(out.String(), deleteStateMessage(env.StateDir)) {
			t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
		}
		if strings.Contains(out.String(), "✗ The pairing didn't finish in time. Run the same command again to start a new one.") {
			t.Errorf("expired result message printed despite state delete failure: %q", safeOutput(out.String()))
		}
		if _, exists, _ := env.Store.Get(); exists {
			t.Error("secret entry should be deleted before state delete")
		}
	})
}

func TestFinishEarlierPairing(t *testing.T) {
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(""))
	backend := local.NewMemoryBackend()
	env.Store = local.NewStore(backend)
	entry := local.Entry{Server: "http://entry-server.example", Credential: secret.FromString(testCredential)}
	if err := local.WriteState(env.StateDir, local.State{MachineID: testMachineID, CredentialSaved: true}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		pathOK := r.URL.Path == "/api/v1/machines/current/acknowledgment"
		proofOK := r.Header.Get("Authorization") == "Bearer "+testCredential
		if !pathOK || !proofOK {
			t.Errorf("request path ok=%t, proof ok=%t", pathOK, proofOK)
		}
		writeResult(w, map[string]string{"result": "ok"})
	}))
	defer server.Close()
	entry.Server = server.URL
	got := finishFlow(time.Now(), time.Second, time.Millisecond).FinishEarlier(context.Background(), env, entry)
	state, exists, err := local.ReadState(env.StateDir)
	if got != 0 || requests.Load() != 1 || err != nil || !exists || !state.Confirmed || !strings.Contains(out.String(), "Finishing the earlier pairing with "+server.URL+"...\nConfirming with the server...\n✓ Paired successfully.\n") {
		t.Errorf("exit=%d requests=%d state=%#v exists=%t err=%v output=%q", got, requests.Load(), state, exists, err, safeOutput(out.String()))
	}
}

func TestFinishEarlierResults(t *testing.T) {
	for _, tc := range []struct{ result, want string }{
		{"expired", "✗ The earlier pairing didn't finish in time. Run the same command again to start a new one.\n"},
		{"failed", "✗ The earlier pairing failed. Run the same command again to start a new one.\n"},
		{"unknown_credential", "✗ The server doesn't recognize the earlier pairing. Run the same command again to start a new one.\n"},
	} {
		t.Run(tc.result, func(t *testing.T) {
			var out bytes.Buffer
			env := finishEnv(t, &out, strings.NewReader(""))
			backend := local.NewMemoryBackend()
			env.Store = local.NewStore(backend)
			entry := local.Entry{Server: "http://entry.example", Credential: secret.FromString(testCredential)}
			_ = local.WriteState(env.StateDir, local.State{MachineID: testMachineID, CredentialSaved: true})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.result == "unknown_credential" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"error":"unknown_credential"}`)
					return
				}
				writeResult(w, map[string]string{"result": tc.result})
			}))
			defer server.Close()
			entry.Server = server.URL
			got := finishFlow(time.Now(), time.Second, time.Millisecond).FinishEarlier(context.Background(), env, entry)
			if got != 1 || !strings.Contains(out.String(), tc.want) {
				t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
			}
			if _, exists := backend.Value("worker"); exists {
				t.Error("entry was not deleted")
			}
			if _, exists, _ := local.ReadState(env.StateDir); exists {
				t.Error("state was not deleted")
			}
		})
	}
}

func TestCtrlCDuringCodePhase(t *testing.T) {
	t.Run("waiting for input", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader := newBlockingReader()
		defer reader.close()
		var out bytes.Buffer
		env := finishEnv(t, &out, reader)
		var codeRequests atomic.Int32
		server := codeServer(t, func(w http.ResponseWriter, _ *http.Request) { codeRequests.Add(1); writeAccepted(w) })
		defer server.Close()
		done := make(chan int, 1)
		go func() { done <- finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(ctx, env, server.URL) }()
		waitReaderStarted(t, reader)
		cancel()
		if got := <-done; got != 130 {
			t.Errorf("exit = %d, want 130", got)
		}
		if !strings.Contains(out.String(), "Pairing cancelled. Nothing was saved.\n") || codeRequests.Load() != 0 {
			t.Errorf("output=%q requests=%d", safeOutput(out.String()), codeRequests.Load())
		}
		if _, exists, _ := local.ReadState(env.StateDir); exists {
			t.Error("state written while waiting for input")
		}
	})
	t.Run("code request running", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
		var codeRequests atomic.Int32
		server := codeServer(t, func(w http.ResponseWriter, r *http.Request) { codeRequests.Add(1); <-r.Context().Done() })
		defer server.Close()
		done := make(chan int, 1)
		go func() { done <- finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(ctx, env, server.URL) }()
		waitAtomic(t, &codeRequests, 1)
		cancel()
		if got := <-done; got != 130 {
			t.Errorf("exit = %d, want 130", got)
		}
		if !strings.Contains(out.String(), "Pairing cancelled. Nothing was saved.\n") || codeRequests.Load() != 1 {
			t.Errorf("output=%q requests=%d", safeOutput(out.String()), codeRequests.Load())
		}
		if _, exists, _ := local.ReadState(env.StateDir); exists {
			t.Error("state written during code request")
		}
	})
}

func TestCtrlCAfterSaving(t *testing.T) {
	t.Run("after write one", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(testCode+"\n"))
		backend := &cancelBackend{MemoryBackend: local.NewMemoryBackend(), cancel: cancel}
		env.Store = local.NewStore(backend)
		var ack atomic.Int32
		server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
				ack.Add(1)
				writeResult(w, map[string]string{"result": "ok"})
				return
			}
			writeAccepted(w)
		})
		defer server.Close()
		got := finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(ctx, env, server.URL)
		if got != 130 || !strings.Contains(out.String(), "Pairing cancelled before it was confirmed.\n  To finish, run:  tervi pair --server "+server.URL+"\n") || ack.Load() != 0 {
			t.Errorf("exit=%d ack=%d output=%q", got, ack.Load(), safeOutput(out.String()))
		}
	})
	t.Run("during earlier confirmation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		env := finishEnv(t, &out, strings.NewReader(""))
		env.Store = local.NewStore(local.NewMemoryBackend())
		entry := local.Entry{Server: "http://placeholder", Credential: secret.FromString(testCredential)}
		_ = local.WriteState(env.StateDir, local.State{MachineID: testMachineID, CredentialSaved: true})
		var acks atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { acks.Add(1); cancel(); <-r.Context().Done() }))
		defer server.Close()
		entry.Server = server.URL
		got := finishFlow(time.Now(), time.Second, time.Millisecond).FinishEarlier(ctx, env, entry)
		if got != 130 || !strings.Contains(out.String(), "Pairing cancelled before it was confirmed.\n  To finish, run:  tervi pair --server "+server.URL+"\n") || acks.Load() != 1 {
			t.Errorf("exit=%d acks=%d output=%q", got, acks.Load(), safeOutput(out.String()))
		}
	})
}

func TestNoSecretInOutput(t *testing.T) {
	server := codeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/machines/current/acknowledgment" {
			writeResult(w, map[string]string{"result": "ok"})
			return
		}
		writeAccepted(w)
	})
	defer server.Close()
	var out bytes.Buffer
	finishFlow(time.Now(), time.Second, time.Millisecond).StartNew(context.Background(), finishEnv(t, &out, strings.NewReader(testCode+"\n")), server.URL)
	for _, secretValue := range []string{testPollingKey, testCredential, testCode} {
		if strings.Contains(out.String(), secretValue) {
			t.Errorf("secret appeared in output: %q", safeOutput(out.String()))
		}
	}
}

func TestFinishEarlierDoesNotUseRequestedServer(t *testing.T) {
	// FinishEarlier receives only the locally saved entry; its server is the sole destination.
	var out bytes.Buffer
	env := finishEnv(t, &out, strings.NewReader(""))
	env.Store = local.NewStore(local.NewMemoryBackend())
	entry := local.Entry{Server: "http://127.0.0.1:1", Credential: secret.FromString(testCredential)}
	_ = local.WriteState(env.StateDir, local.State{MachineID: testMachineID, CredentialSaved: true})
	got := finishFlow(time.Now(), 20*time.Millisecond, time.Millisecond).FinishEarlier(context.Background(), env, entry)
	if got != 1 || !strings.Contains(out.String(), "Finishing the earlier pairing with "+entry.Server) {
		t.Errorf("exit=%d output=%q", got, safeOutput(out.String()))
	}
}

func finishFlow(now time.Time, requestTimeout, pollInterval time.Duration) *pairingFlow {
	return newFlow(Options{RequestTimeout: requestTimeout, PollInterval: pollInterval, Now: func() time.Time { return now }})
}

func finishEnv(t *testing.T, out io.Writer, stdin io.Reader) local.Env {
	t.Helper()
	return local.Env{Stdin: stdin, Stdout: out, Stderr: io.Discard, Store: local.NewStore(local.NewMemoryBackend()), StateDir: filepath.Join(t.TempDir(), "state")}
}

func codeServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return codeServerWithExpiry(t, 600, handler)
}

func codeServerWithExpiry(t *testing.T, expires int, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	return codeServerWithExpiryAndTries(t, expires, 5, handler)
}

func codeServerWithExpiryAndTries(t *testing.T, expires, triesLeft int, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case startPath:
			writeStart(w, testPollingKey, serverURL(r)+"/pair/"+testApprovalKey, 600)
		case pollPath:
			writePoll(w, "waiting_for_code", expires, intPointer(triesLeft))
		case "/api/v1/pairings/current/code":
			if r.Header.Get("Authorization") != "Bearer "+testPollingKey {
				t.Errorf("code proof mismatch")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode code body: %v", err)
			}
			if body["code"] == "" {
				t.Errorf("code body is empty")
			}
			if handler == nil {
				writeAccepted(w)
			} else {
				handler(w, r)
			}
		case "/api/v1/machines/current/acknowledgment", "/api/v1/machines/current/save-failure":
			if handler != nil {
				handler(w, r)
			} else {
				writeResult(w, map[string]string{"result": "ok"})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func writeAccepted(w http.ResponseWriter) {
	writeResult(w, map[string]string{"result": "accepted", "credential": testCredential, "machine_id": testMachineID})
}

func writeResult(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func deleteStateMessage(dir string) string {
	return fmt.Sprintf("✗ Can't delete %s.\n  Check that you can change files in %s, then run the same command again.\n", local.StatePath(dir), dir)
}

func makeStateDirReadOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Errorf("make state directory read-only: %v", err)
		return
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
}

type sabotageBackend struct {
	*local.MemoryBackend
	onSet func()
}

func (b *sabotageBackend) Set(user, value string) error {
	err := b.MemoryBackend.Set(user, value)
	if b.onSet != nil {
		b.onSet()
	}
	return err
}

type cancelBackend struct {
	*local.MemoryBackend
	cancel context.CancelFunc
}

type recordingBackend struct {
	backend local.Backend
	observe func(string)
}

func (b *recordingBackend) Get(user string) (string, error) {
	value, err := b.backend.Get(user)
	if err == nil && b.observe != nil {
		b.observe("store read back")
	}
	return value, err
}

func (b *recordingBackend) Set(user, value string) error {
	err := b.backend.Set(user, value)
	if err == nil && b.observe != nil {
		b.observe("store set")
	}
	return err
}

func (b *recordingBackend) Delete(user string) error { return b.backend.Delete(user) }

func (b *cancelBackend) Set(user, value string) error {
	err := b.MemoryBackend.Set(user, value)
	b.cancel()
	return err
}

type blockingReader struct {
	once      sync.Once
	startOnce sync.Once
	started   chan struct{}
	done      chan struct{}
}

type delayedReader struct {
	reader *bytes.Reader
	delay  time.Duration
	once   sync.Once
}

func (r *delayedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { time.Sleep(r.delay) })
	return r.reader.Read(p)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func newBlockingReader() *blockingReader {
	return &blockingReader{started: make(chan struct{}), done: make(chan struct{})}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.done
	return 0, io.EOF
}

func (r *blockingReader) close() { r.once.Do(func() { close(r.done) }) }

func waitAtomic(t *testing.T, value *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("counter = %d, want at least %d", value.Load(), want)
}

func waitReaderStarted(t *testing.T, reader *blockingReader) {
	t.Helper()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("code phase did not begin reading standard input")
	}
}
