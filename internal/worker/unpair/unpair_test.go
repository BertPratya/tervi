package unpair

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
)

const unpairTestCredential = "unpair-secret-must-not-be-printed"

type unpairTestRunner struct {
	calls  [][]string
	output string
	err    error
	fail   map[string]error
	events *[]string
	onCall func(name string, args []string)
}

func (runner *unpairTestRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	runner.calls = append(runner.calls, call)
	if runner.events != nil {
		*runner.events = append(*runner.events, strings.Join(call, " "))
	}
	if runner.onCall != nil {
		runner.onCall(name, args)
	}
	if err := runner.fail[strings.Join(call, " ")]; err != nil {
		return "", err
	}
	return runner.output, runner.err
}

func (*unpairTestRunner) Stream(context.Context, io.Writer, string, ...string) error { return nil }

type unpairFixture struct {
	t             *testing.T
	root          string
	stateDir      string
	serviceDir    string
	runtimeBase   string
	programPath   string
	runner        *unpairTestRunner
	server        *httptest.Server
	requests      atomic.Int32
	authorization string
	status        atomic.Int32
	client        *cli.Env
	stdout        *strings.Builder
	stderr        *strings.Builder
}

func newUnpairFixture(t *testing.T, setup func(http.ResponseWriter, *http.Request)) *unpairFixture {
	t.Helper()
	fixture := &unpairFixture{t: t, root: t.TempDir()}
	fixture.stateDir = filepath.Join(fixture.root, "config", "tervi")
	fixture.serviceDir = filepath.Join(fixture.root, "config", "systemd", "user")
	fixture.runtimeBase = filepath.Join(fixture.root, "runtime")
	fixture.programPath = filepath.Join(fixture.root, "bin", "tervi")
	if err := os.MkdirAll(fixture.runtimeBase, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fixture.programPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.programPath, []byte("installed tervi program"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", fixture.runtimeBase)
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)
		fixture.authorization = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/machines/current/unpair" {
			t.Errorf("request = %s %s, want unpair endpoint", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if setup != nil {
			setup(w, r)
			return
		}
		status := int(fixture.status.Load())
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(fixture.server.Close)
	fixture.status.Store(http.StatusNoContent)
	fixture.runner = &unpairTestRunner{}
	fixture.stdout = &strings.Builder{}
	fixture.stderr = &strings.Builder{}
	fixture.client = &cli.Env{
		Stdin: strings.NewReader("y\n"), Stdout: fixture.stdout, Stderr: fixture.stderr,
		Store: credential.NewStore(credential.NewFileBackend(fixture.stateDir)), StateDir: fixture.stateDir,
		Runner: fixture.runner, ProgramPath: fixture.programPath, ServiceDir: fixture.serviceDir,
	}
	return fixture
}

func (fixture *unpairFixture) pair(t *testing.T, confirmed bool, withState bool) {
	t.Helper()
	if withState {
		if err := state.WriteState(fixture.stateDir, state.State{MachineID: "machine-1", CredentialSaved: true, Confirmed: confirmed}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.client.Store.Save(credential.Entry{Server: fixture.server.URL, Credential: secret.FromString(unpairTestCredential)}); err != nil {
		t.Fatal(err)
	}
}

func (fixture *unpairFixture) serviceFile(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll(fixture.serviceDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.serviceDir, "tervi.service")
	if err := os.WriteFile(path, []byte("service"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (fixture *unpairFixture) run(args ...string) int {
	fixture.stdout.Reset()
	fixture.stderr.Reset()
	fixture.client.Stdin = strings.NewReader("y\n")
	fixture.client.Unpairer = New(Options{RequestTimeout: 30 * time.Millisecond, LockWait: 15 * time.Millisecond})
	return cli.Run(context.Background(), args, *fixture.client, nil)
}

func (fixture *unpairFixture) assertNoPairingFiles(t *testing.T) {
	t.Helper()
	for _, path := range []string{credential.Path(fixture.stateDir), state.StatePath(fixture.stateDir), filepath.Join(fixture.serviceDir, "tervi.service"), filepath.Join(fixture.stateDir, "credential-check.json")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains: %v", path, err)
		}
	}
}

func TestUnpairRemovesInOrder(t *testing.T) {
	var events []string
	fixture := newUnpairFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		events = append(events, "server")
		w.WriteHeader(http.StatusNoContent)
	})
	fixture.runner.events = &events
	fixture.pair(t, true, true)
	servicePath := fixture.serviceFile(t)
	fixture.runner.onCall = func(name string, args []string) {
		if name == "systemctl" && len(args) > 0 && args[len(args)-1] == "daemon-reload" {
			if _, err := os.Stat(filepath.Join(fixture.runtimeBase, "tervi", "daemon.lock")); err != nil {
				t.Errorf("daemon-reload ran without daemon.lock held: %v", err)
			}
		}
	}
	code := fixture.run("unpair", "--yes")
	want := "✓ Unpaired. This computer is no longer paired with " + fixture.server.URL + ".\n"
	if code != 0 || fixture.stdout.String() != want || fixture.stderr.Len() != 0 {
		t.Fatalf("Run(unpair) = %d, stdout=%q stderr=%q", code, fixture.stdout.String(), fixture.stderr.String())
	}
	if fixture.authorization != "Bearer "+unpairTestCredential || fixture.requests.Load() != 1 {
		t.Errorf("request auth=%q count=%d", fixture.authorization, fixture.requests.Load())
	}
	wantEvents := []string{"server", "systemctl --user disable --now tervi", "systemctl --user daemon-reload", "loginctl show-user --property=Linger --value"}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Errorf("events = %v, want %v", events, wantEvents)
	}
	fixture.assertNoPairingFiles(t)
	if _, err := os.Stat(fixture.stateDir); err != nil {
		t.Errorf("state directory was removed: %v", err)
	}
	if data, err := os.ReadFile(fixture.programPath); err != nil || string(data) != "installed tervi program" {
		t.Errorf("installed program changed: data=%q err=%v", data, err)
	}
	if got := fixture.runner.calls[0]; fmt.Sprint(got) != "[systemctl --user disable --now tervi]" {
		t.Errorf("first service call = %v", got)
	}
	if _, err := os.Stat(servicePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("service file remains: %v", err)
	}
}

func TestUnpairNoAnswer(t *testing.T) {
	var answer atomic.Bool
	var requests atomic.Int32
	fixture := newUnpairFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !answer.Load() {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	fixture.pair(t, true, true)
	servicePath := fixture.serviceFile(t)
	code := fixture.run("unpair", "--yes")
	want := "✗ No answer from " + fixture.server.URL + ".\n  The server may or may not have unpaired this computer. Nothing was removed here.\n  Run tervi unpair again to finish, or revoke this computer on the Machines page.\n"
	if code != 1 || fixture.stdout.String() != want || fixture.stderr.Len() != 0 {
		t.Errorf("Run(no answer) = %d, stdout=%q stderr=%q", code, fixture.stdout.String(), fixture.stderr.String())
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want one", requests.Load())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
		t.Errorf("credential removed after no answer: %v", err)
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Errorf("service removed after no answer: %v", err)
	}
	answer.Store(true)
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("second Run(unpair) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if requests.Load() != 2 {
		t.Errorf("requests after retry = %d, want two total", requests.Load())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairCannotConnect(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	servicePath := fixture.serviceFile(t)
	fixture.server.Close()
	want := "✗ No answer from " + fixture.server.URL + ".\n  The server may or may not have unpaired this computer. Nothing was removed here.\n  Run tervi unpair again to finish, or revoke this computer on the Machines page.\n"
	if code := fixture.run("unpair", "--yes"); code != 1 || fixture.stdout.String() != want {
		t.Errorf("Run(cannot connect) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
		t.Errorf("credential removed when server was unavailable: %v", err)
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Errorf("service removed when server was unavailable: %v", err)
	}
}

func TestUnpairOtherAnswer(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	fixture.serviceFile(t)
	fixture.status.Store(http.StatusInternalServerError)
	code := fixture.run("unpair", "--yes")
	want := "✗ " + fixture.server.URL + " couldn't unpair this computer (HTTP 500). Nothing was removed here.\n  Run tervi unpair again to finish, or revoke this computer on the Machines page.\n"
	if code != 1 || fixture.stdout.String() != want {
		t.Errorf("Run(unpair 500) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
		t.Errorf("credential removed after 500: %v", err)
	}
	fixture.status.Store(http.StatusNoContent)
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after 500 = %d: %q", code, fixture.stdout.String())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairDoesNotFollowRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	fixture := newUnpairFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	fixture.pair(t, true, true)
	fixture.serviceFile(t)
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "HTTP 307") {
		t.Errorf("Run(redirect) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 1 || targetRequests.Load() != 0 {
		t.Errorf("redirect requests = initial %d, target %d; want 1, 0", fixture.requests.Load(), targetRequests.Load())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
		t.Errorf("credential removed after redirect: %v", err)
	}
}

func TestUnpairServerAlreadyRefuses(t *testing.T) {
	for _, body := range []string{`{"error":"unknown_credential"}`, `{"error":"credential_revoked"}`, `anything else`} {
		t.Run(body, func(t *testing.T) {
			fixture := newUnpairFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, body)
			})
			fixture.pair(t, true, true)
			fixture.serviceFile(t)
			if code := fixture.run("unpair", "--yes"); code != 0 {
				t.Fatalf("Run(unpair 401) = %d, stdout=%q", code, fixture.stdout.String())
			}
			fixture.assertNoPairingFiles(t)
		})
	}
}

func TestUnpairLocalOnly(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	fixture.serviceFile(t)
	if code := fixture.run("unpair", "--local-only"); code != 0 {
		t.Fatalf("Run(local-only) = %d, stdout=%q", code, fixture.stdout.String())
	}
	want := "This removes this computer's pairing without telling " + fixture.server.URL + ".\nIf that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]\n✓ Unpaired on this computer only.\n"
	if fixture.stdout.String() != want || fixture.requests.Load() != 0 {
		t.Errorf("local-only output=%q requests=%d", fixture.stdout.String(), fixture.requests.Load())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairLingerLine(t *testing.T) {
	for _, tc := range []struct {
		name       string
		output     string
		failure    bool
		wantLinger bool
	}{
		{name: "on", output: "yes\n", wantLinger: true},
		{name: "off", output: "no\n"},
		{name: "failure", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newUnpairFixture(t, nil)
			fixture.pair(t, true, true)
			fixture.runner.output = tc.output
			if tc.failure {
				fixture.runner.fail = map[string]error{"loginctl show-user --property=Linger --value": errors.New("loginctl unavailable")}
			}
			if code := fixture.run("unpair", "--yes"); code != 0 {
				t.Fatalf("Run(unpair) = %d, stdout=%q", code, fixture.stdout.String())
			}
			gotLine := strings.Contains(fixture.stdout.String(), "  Linger is still on. Turn it off with: loginctl disable-linger\n")
			if gotLine != tc.wantLinger {
				t.Errorf("linger line = %v, stdout=%q", gotLine, fixture.stdout.String())
			}
		})
	}
}

func TestUnpairNotPaired(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	if code := fixture.run("unpair", "--yes"); code != 1 || fixture.stdout.String() != "This computer isn't paired. Nothing was changed.\n" {
		t.Errorf("Run(not paired) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 0 || len(fixture.runner.calls) != 0 {
		t.Errorf("not-paired command did work: requests=%d runner=%v", fixture.requests.Load(), fixture.runner.calls)
	}
}

func TestUnpairUnreadableCredential(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode 000 credential files")
	}
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	path := credential.Path(fixture.stateDir)
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 1 || fixture.stdout.String() != "✗ Can't read "+path+".\n" {
		t.Errorf("Run(unreadable) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 0 || len(fixture.runner.calls) != 0 {
		t.Errorf("unreadable credential caused work: requests=%d runner=%v", fixture.requests.Load(), fixture.runner.calls)
	}
}

func TestUnpairDamagedState(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, false)
	if err := os.WriteFile(state.StatePath(fixture.stateDir), []byte("damaged state"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("Run(damaged state) = %d, stdout=%q", code, fixture.stdout.String())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairNoRuntimeFolder(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	servicePath := fixture.serviceFile(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	want := "✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).\n"
	if code := fixture.run("unpair", "--yes"); code != 1 || fixture.stdout.String() != want {
		t.Errorf("Run(no runtime) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 0 || len(fixture.runner.calls) != 0 {
		t.Errorf("no-runtime command did work: requests=%d runner=%v", fixture.requests.Load(), fixture.runner.calls)
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Errorf("service changed without runtime dir: %v", err)
	}
}

func TestUnpairLeftovers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		localOnly   bool
		credential  string
		mode        os.FileMode
		withService bool
		withState   bool
	}{
		{name: "service and state without credential", withService: true, withState: true},
		{name: "damaged credential", credential: "not-json"},
		{name: "exposed credential", credential: "valid", mode: 0644, localOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newUnpairFixture(t, nil)
			if tc.withState {
				if err := state.WriteState(fixture.stateDir, state.State{MachineID: "machine"}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.credential != "" {
				if err := os.MkdirAll(fixture.stateDir, 0700); err != nil {
					t.Fatal(err)
				}
				data := []byte(tc.credential)
				if tc.credential == "valid" {
					data = []byte(`{"server":"` + fixture.server.URL + `","credential":"` + unpairTestCredential + `"}`)
				}
				if err := os.WriteFile(credential.Path(fixture.stateDir), data, 0600); err != nil {
					t.Fatal(err)
				}
				if tc.mode != 0 {
					if err := os.Chmod(credential.Path(fixture.stateDir), tc.mode); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.withService {
				fixture.serviceFile(t)
			}
			args := []string{"unpair", "--yes"}
			if tc.localOnly {
				args = append(args, "--local-only")
			}
			if code := fixture.run(args...); code != 0 {
				t.Fatalf("Run(leftovers) = %d, stdout=%q", code, fixture.stdout.String())
			}
			if !strings.Contains(fixture.stdout.String(), "✓ Removed what was left of the earlier pairing.") {
				t.Errorf("leftovers success line absent: %q", fixture.stdout.String())
			}
			if fixture.requests.Load() != 0 {
				t.Errorf("leftovers contacted server %d times", fixture.requests.Load())
			}
			fixture.assertNoPairingFiles(t)
		})
	}
}

func TestUnpairUnfinishedPairing(t *testing.T) {
	for _, withState := range []bool{false, true} {
		t.Run(fmt.Sprintf("state=%v", withState), func(t *testing.T) {
			fixture := newUnpairFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"unknown_credential"}`)
			})
			fixture.pair(t, false, withState)
			if code := fixture.run("unpair", "--yes"); code != 0 {
				t.Fatalf("Run(unfinished pairing) = %d, stdout=%q", code, fixture.stdout.String())
			}
			if fixture.requests.Load() != 1 || !strings.Contains(fixture.stdout.String(), "✓ Unpaired. This computer is no longer paired with") {
				t.Errorf("unfinished pairing result requests=%d stdout=%q", fixture.requests.Load(), fixture.stdout.String())
			}
			fixture.assertNoPairingFiles(t)
		})
	}
}

func TestUnpairServiceFailure(t *testing.T) {
	for _, failure := range []string{"systemctl --user disable --now tervi", "systemctl --user daemon-reload"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newUnpairFixture(t, nil)
			fixture.pair(t, true, true)
			servicePath := fixture.serviceFile(t)
			fixture.runner.fail = map[string]error{failure: errors.New("systemctl failed")}
			if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "✗ Can't stop tervi in the background: systemctl failed\n  Finish with: tervi unpair --local-only\n") {
				t.Errorf("Run(service failure) = %d, stdout=%q", code, fixture.stdout.String())
			}
			if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
				t.Errorf("credential was removed on service failure: %v", err)
			}
			delete(fixture.runner.fail, failure)
			if code := fixture.run("unpair", "--yes"); code != 0 {
				t.Fatalf("retry after service failure = %d, stdout=%q", code, fixture.stdout.String())
			}
			if _, err := os.Stat(servicePath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("service file remains after retry: %v", err)
			}
			fixture.assertNoPairingFiles(t)
		})
	}
}

func TestUnpairServiceFileDeleteFailure(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	servicePath := fixture.serviceFile(t)
	if err := os.Remove(servicePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(servicePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicePath, "child"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "✗ Can't delete "+servicePath+":") {
		t.Errorf("Run(service file delete failure) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); err != nil {
		t.Errorf("credential was removed after service file failure: %v", err)
	}
	if err := os.Remove(filepath.Join(servicePath, "child")); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after service file failure = %d, stdout=%q", code, fixture.stdout.String())
	}
	fixture.assertNoPairingFiles(t)
}

type failDeleteBackend struct {
	credential.Backend
	fail bool
}

func (backend *failDeleteBackend) Delete(user string) error {
	if backend.fail && user == "worker" {
		backend.fail = false
		return errors.New("delete failed")
	}
	return backend.Backend.Delete(user)
}

func TestUnpairDeleteFailure(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	base := credential.NewFileBackend(fixture.stateDir)
	backend := &failDeleteBackend{Backend: base, fail: true}
	fixture.client.Store = credential.NewStore(backend)
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "✗ Can't delete "+credential.Path(fixture.stateDir)+":") || !strings.Contains(fixture.stdout.String(), "  Finish with: tervi unpair --local-only\n") {
		t.Errorf("Run(delete failure) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(state.StatePath(fixture.stateDir)); err != nil {
		t.Errorf("state was deleted after credential failure: %v", err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after delete failure = %d, stdout=%q", code, fixture.stdout.String())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairDeleteLeftoverFailure(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	leftover := filepath.Join(fixture.stateDir, ".credential-blocked")
	if err := os.Mkdir(leftover, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(leftover, "child")
	if err := os.WriteFile(child, []byte(unpairTestCredential), 0600); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "✗ Can't delete "+leftover+":") || !strings.Contains(fixture.stdout.String(), "  Finish with: tervi unpair --local-only\n") {
		t.Errorf("Run(leftover delete failure) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(state.StatePath(fixture.stateDir)); err != nil {
		t.Errorf("state was deleted after leftover failure: %v", err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after leftover failure = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 1 {
		t.Errorf("retry contacted server after the credential was removed: %d requests", fixture.requests.Load())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairDeleteStateFailure(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	statePath := state.StatePath(fixture.stateDir)
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(statePath, "child")
	if err := os.WriteFile(child, []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "✗ Can't delete "+statePath+":") || !strings.Contains(fixture.stdout.String(), "  Finish with: tervi unpair --local-only\n") {
		t.Errorf("Run(state delete failure) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if _, err := os.Stat(credential.Path(fixture.stateDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credential remains after state failure: %v", err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after state failure = %d, stdout=%q", code, fixture.stdout.String())
	}
	if fixture.requests.Load() != 1 {
		t.Errorf("retry contacted server after the credential was removed: %d requests", fixture.requests.Load())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairLockFailure(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	fixture.serviceFile(t)
	runtimeDir := filepath.Join(fixture.runtimeBase, "tervi")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(runtimeDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 1 || !strings.Contains(fixture.stdout.String(), "Can't stop tervi in the background") {
		t.Errorf("Run(lock failure) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("retry after lock failure = %d, stdout=%q", code, fixture.stdout.String())
	}
	fixture.assertNoPairingFiles(t)
}

func TestUnpairRemovesSecretLeftoversAndKeepsOtherFiles(t *testing.T) {
	for _, localOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("local-only=%v", localOnly), func(t *testing.T) {
			fixture := newUnpairFixture(t, nil)
			fixture.pair(t, true, true)
			if err := os.WriteFile(filepath.Join(fixture.stateDir, ".credential-x1y2"), []byte(unpairTestCredential), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fixture.stateDir, "credential-check.json"), []byte("check"), 0600); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(fixture.stateDir, "notes.txt")
			if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"unpair", "--yes"}
			if localOnly {
				args = append(args, "--local-only")
			}
			if code := fixture.run(args...); code != 0 {
				t.Fatalf("Run(unpair) = %d, stdout=%q", code, fixture.stdout.String())
			}
			if _, err := os.Stat(other); err != nil {
				t.Errorf("unrelated file removed: %v", err)
			}
			if _, err := os.Stat(fixture.stateDir); err != nil {
				t.Errorf("state directory removed: %v", err)
			}
			fixture.assertNoPairingFiles(t)
			if strings.Contains(fixture.stdout.String(), unpairTestCredential) {
				t.Errorf("output contains credential: %q", fixture.stdout.String())
			}
			entries, err := os.ReadDir(fixture.stateDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(fixture.stateDir, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), unpairTestCredential) {
					t.Errorf("file %s still contains credential", entry.Name())
				}
			}
			wantRequests := int32(1)
			if localOnly {
				wantRequests = 0
			}
			if fixture.requests.Load() != wantRequests {
				t.Errorf("server requests = %d, want %d", fixture.requests.Load(), wantRequests)
			}
		})
	}
}

func TestUnpairNoSecret(t *testing.T) {
	fixture := newUnpairFixture(t, nil)
	fixture.pair(t, true, true)
	if err := os.WriteFile(filepath.Join(fixture.stateDir, ".credential-copy"), []byte(unpairTestCredential), 0600); err != nil {
		t.Fatal(err)
	}
	if code := fixture.run("unpair", "--yes"); code != 0 {
		t.Fatalf("Run(unpair) = %d, stdout=%q", code, fixture.stdout.String())
	}
	if strings.Contains(fixture.stdout.String(), unpairTestCredential) {
		t.Errorf("output contains credential: %q", fixture.stdout.String())
	}
	err := filepath.WalkDir(fixture.stateDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), unpairTestCredential) {
			return fmt.Errorf("%s still contains credential", path)
		}
		return nil
	})
	if err != nil {
		t.Errorf("credential remains in config folder: %v", err)
	}
}
