# Task 05 — Worker: the background service, `tervi connect`, `tervi status`, `tervi logs`

Slice: 0002-connection    Risk: core    Depends on: 04

## Goal

Set up tervi as a systemd user service and start it, and add the three
commands people use with a running daemon: `tervi connect`, `tervi status`,
and `tervi logs`. Each command does its work, prints its last line, and
exits, leaving the daemon running.

## Requirements (copied from the spec)

- R4. When the user runs `tervi connect` on a paired computer, the worker shall
  set `connect` to true, set up and start the background service if it is
  missing or stopped (as in R5), and print `✓ Connected.` once connected. If
  it is already connected, the worker shall print `Already connected.` and
  change nothing. On a computer that is not paired, it shall print
  `This computer isn't paired. Pair it with: tervi pair --server <url>` and
  change nothing. When it is not connected:
  - If the daemon has not connected within 30 seconds, `tervi connect` shall
    print `Not connected yet: <last error>. tervi keeps trying in the background.`
    and `Check with: tervi status`. The daemon keeps trying (R2).
  - If the server rejects the credential (R20), it shall print
    `✗ The server doesn't accept this computer's credential.` and
    `Pair again with: tervi pair --server <server>`.
  - If the background service cannot be set up or started, it shall say so
    with the reason and print `Try again with: tervi connect`.

  `tervi connect` shall exit with `0` when connected or already connected,
  and `1` otherwise.
- R5. When pairing succeeds, the worker shall set up the daemon as a systemd
  user service named `tervi`, start it, and enable it so that it starts every
  time the user logs in. The service shall run the same `tervi` program file
  that set it up. *This task: setting up, starting, and enabling.*
- R6. If the daemon stops unexpectedly, systemd shall start it again after a
  few seconds. *This task: the service file says so.*
- R16. `tervi status` shall show one of:
  - `● Connected to <server>`, with the machine name and since when;
  - `○ Reconnecting to <server>`, with the try number, the wait before the
    next try, and the last error;
  - `✗ tervi isn't running in the background.` with `Start it with: tervi connect`;
  - `✗ This computer isn't paired.` with `Pair it with: tervi pair --server <url>`;
  - `✗ Not connecting: the server doesn't accept this computer's credential.`
    with `Pair again with: tervi pair --server <server>`.
- R17. `tervi status` shall exit with `0` when connected, and `1` otherwise.
- R28. If `tervi pair` or `tervi connect` runs from a temporary program file
  (as `go run` creates), then it shall print
  `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi`
  and stop. `tervi pair` shall do this before contacting the server. *This
  task: the check, and `tervi connect`.*
- R29. If the background service cannot be set up or started (for example,
  systemd is not available), then the worker shall say so with the reason,
  keep the pairing, and print `Try again with: tervi connect`. *This task:
  for `tervi connect`, and the shared start step.*
- R31. When `tervi pair` or `tervi connect` has printed its last line, it
  shall exit and give the prompt back, leaving the daemon running in the
  background. Closing that terminal, or running other commands in it, shall
  not stop the daemon or its connection. The user works with the running
  daemon through `tervi` commands, such as `tervi status` and
  `tervi connect`. *This task: `tervi connect`.*
- R32. When the user runs `tervi logs`, the worker shall print the
  background daemon's log so far and exit. With `-f`, it shall then keep
  printing new lines as they are written, until the user presses Ctrl+C.
  The log of a daemon run by hand with `tervi daemon` is printed in its own
  terminal instead (R30).
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`. *This task: the output of the three
  commands.*

## Context

### What exists (tasks 03, 04)

- `internal/worker/control`: the runtime folder path function, and a client
  with `Status(ctx)` and `Connect(ctx)` that return `ErrNotRunning` when no
  daemon answers.
- `internal/worker/cli`: one file per command; `connect`, `status`, and
  `logs` print `Not available yet.` — replace them.
- `credential.Paired(stateDir, store)` (task 03) tells whether this computer
  is paired, and gives the secret store entry, whose `server` is the
  `<server>` in every message below. Use it; do not write a second check.
- The daemon's `last error` texts are already in the form the messages
  below need (`connection refused`, lower-case).

### The package: `internal/worker/service`

All systemd work, behind a `Runner` interface so tests never call real
systemd:

```go
type Runner interface {
    // Run runs a command to the end and returns its combined output.
    Run(ctx context.Context, name string, args ...string) (output string, err error)
    // Stream runs a command with its standard output and standard error
    // going to out as they are written, until it ends or ctx ends.
    Stream(ctx context.Context, out io.Writer, name string, args ...string) error
}
```

The real `Runner` uses `os/exec`. `tervi logs` uses `Stream`; everything
else uses `Run`.

**Installed check (R28).** `os.Executable()`, with symbolic links resolved.
If it lies inside `os.TempDir()`, or inside any folder named `go-build`
(where `go run` builds programs, even when `GOTMPDIR` moves Go's temporary
folder), the program is temporary: print
`tervi must be installed to run in the background. Install it with: go install ./cmd/tervi`
and exit `1`.

**The service file.** `tervi.service` in the service-file folder, exactly the
contents in `design/data-model.md`, with
`ExecStart="<absolute program path>" daemon`: the path in double quotes,
with `\` and `"` inside it written `\\` and `\"`, and `%` and `$` written
`%%` and `$$` (decided by the user during the slice review, so a path with a
space works). Reading the file back to compare paths undoes the same
escaping. Create the folder if needed. The
folder is a field of the service options and of `cli.Env`
(`ServiceDir`); `main` sets it to `<os.UserConfigDir()>/systemd/user`,
which is `~/.config/systemd/user`.

**Tests never touch the real folder.** Every test that can reach the service
code (in this task, task 06, and task 07) sets `ServiceDir` to
`t.TempDir()`. A test that left it empty would write over the service file
the user set up by hand, so an empty `ServiceDir` is a programming error:
`EnsureRunning` returns an error and writes nothing.

**Start (the "Starting the background service" mechanism in `plan.md`):**

1. If the service file is missing or its `ExecStart` names another path:
   write it, then run `systemctl --user daemon-reload`,
   `systemctl --user enable tervi`, and `systemctl --user restart tervi`
   (a daemon still running an old program file is replaced, R5).
   Otherwise run `systemctl --user enable --now tervi`.
2. Wait until `control.Client.Status` stops returning `ErrNotRunning`,
   checking every 200 ms, at most 10 seconds.

A failure in any step returns an error whose text is systemd's own message
when there is one (the command's output, trimmed), or
`the daemon did not start within 10 seconds`. The caller prints
`✗ Can't start tervi in the background: <reason>` and
`Try again with: tervi connect`.

Export one function that the pairing (task 06) also uses:
`EnsureRunning(ctx) (bool, error)` doing steps 1–2; the `bool` is true when
this call started or restarted the daemon: it rewrote the service file and
restarted it, or one `Status` call made before `enable --now` returned
`ErrNotRunning` (added in fixes 2 and 3, for the `already_connected` row
below). A daemon that was already running, including one started by hand
with `tervi daemon`, gives `false`. Put whatever the commands need
into `cli.Env`, so tests can replace them: the `Runner`, the program path,
the service-file folder (`ServiceDir`), and the control client.

Added during task 05 review:
- In `internal/worker/cli`, one helper `ensureRunning(ctx, env) (bool, error)`
  builds the service's settings from `cli.Env` and calls `EnsureRunning`;
  `tervi connect` and task 06's hand-over both use it.
- The installed check lives in `internal/worker/service` as an exported
  function, reusing the same path resolution as the service file; the
  temporary folder's symbolic links are resolved too.
- **No runtime folder** (decided by the user): when `XDG_RUNTIME_DIR` is not
  set, `tervi status` and `tervi connect` print
  `✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).`
  and exit `1`, before doing anything else. They never look for the socket
  in another folder.
- **No program file** (decided by the user): when `os.Executable()` fails,
  `main` prints `✗ Can't find tervi's program file.` and exits `1`.

`main` in `cmd/tervi` sets all four to their real values. `cli.Run` never
fills in a default: if `connect`, `status`, `logs`, or the pairing hand-over
finds any of the four it needs empty or nil, it prints
`✗ tervi is missing an internal setting (<field name>).` and exits `1`,
without calling systemd or writing a file. Tests set fakes, and
`t.TempDir()` for `ServiceDir`. The checks for the four that starting the
service needs (program path, `Runner`, `ServiceDir`, control client) are
written once, in or next to `ensureRunning(ctx, env)`, so `tervi connect`
and task 06's hand-over share them (added during task 05 review).

Put the printing of the shared messages (the two R29 lines, the two
rejected lines, the not-paired line, the two control-error lines) in
functions in `internal/worker/cli`, so task 06 reuses them.

### When the pairing cannot be read

`credential.Paired` returns an error when the secret store cannot be read
(for example, the keyring is still locked), when the entry is damaged, or
when `state.json` cannot be read or parsed. Both `tervi connect` and
`tervi status` then print slice 1's existing message for that failure and
exit `1`; there is no new wording:

| Error | Output |
| --- | --- |
| The secret store cannot be read, or its entry is damaged | `printStoreReadFailure` in `internal/worker/cli/cli.go`: `✗ Can't read the secret store entry.` then `  Make sure you are logged in to a desktop session and the keyring is unlocked.` |
| `state.json` cannot be read or parsed | `✗ Can't read <path of state.json>.`, the same text `cli.Run` prints today in step zero. Move that `fmt.Fprintf` into a function `printStateReadFailure` and use it in both places |

Neither command writes, deletes, or starts anything in these cases.

### `tervi connect` (R4, R28, R29, R31)

1. Installed check (R28).
2. `credential.Paired`: an error → "When the pairing cannot be read" above.
   Not paired → `This computer isn't paired. Pair it with: tervi pair --server <url>`, exit `1`.
3. `EnsureRunning` → on failure, the two lines above, exit `1`. The pairing
   is untouched.
4. `control.Client.Connect` and print:

| Result | Output | Exit |
| --- | --- | --- |
| `connected` | `✓ Connected.` | `0` |
| `already_connected` | `Already connected.` — but `✓ Connected.` when step 3 started or restarted the daemon: the service file was rewritten (a different installed `tervi`; decided by the user during the slice review, spec "Decisions"), or the daemon was not running (R4: a stopped service is started, then `✓ Connected.`; added in fix 3). A freshly started daemon often connects before `POST /connect` arrives, so its answer alone cannot tell. `EnsureRunning` tells its caller whether it started or restarted the daemon | `0` |
| `still_trying` | `Not connected yet: <last error>. tervi keeps trying in the background.` then `Check with: tervi status` | `1` |
| `still_trying` with no last error (no try has failed yet) | `Not connected yet. tervi keeps trying in the background.` then `Check with: tervi status` (decided by the user during task 03) | `1` |
| `failed` (`state.json` could not be written) | `✗ Can't write <path>.` (decided by the user during task 03) | `1` |
| `rejected` | `✗ The server doesn't accept this computer's credential.` then `Pair again with: tervi pair --server <server>` | `1` |
| `not_paired` | the not-paired line above | `1` |
| a control error (the service started, but the call failed) | `✗ Can't reach tervi in the background: <reason>` then `Check with: tervi status` | `1` |

Ctrl+C while waiting for the answer prints
`Connecting was cancelled; tervi keeps trying in the background.` and exits
`130`. Ctrl+C stops only the waiting, never the daemon.

`<server>` comes from the secret store entry. In
`Pair it with: tervi pair --server <url>`, `<url>` is printed exactly as
written, because a computer that is not paired has no server address to fill
in. The command then exits; it never stops the daemon.

### `tervi status` (R16, R17)

1. `credential.Paired`: an error → "When the pairing cannot be read" above.
   Not paired → `✗ This computer isn't paired.` then
   `  Pair it with: tervi pair --server <url>` (`<url>` printed as is),
   exit `1`. A daemon that answers `not_paired` gets the same two lines.
2. `control.Client.Status`; `ErrNotRunning` →
   `✗ tervi isn't running in the background.` then
   `  Start it with: tervi connect`, exit `1`. Any other error (the daemon
   answered, but the answer is broken) →
   `✗ Can't reach tervi in the background: <reason>` alone, with no
   `Check with` line, exit `1` (decided by the user during task 05). An
   answer with a `state` that is none of the four is a broken answer too.
   Ctrl+C while waiting for the answer prints nothing and exits `130`
   (decided by the user during task 05).
3. Otherwise:

```text
● Connected to http://localhost:8080
  Machine: bert-desktop
  Since:   10:42 (12 minutes)
```

```text
○ Reconnecting to http://localhost:8080
  Try 3, next in 4 seconds
  Last error: connection refused
```

```text
✗ Not connecting: the server doesn't accept this computer's credential.
  Pair again with: tervi pair --server http://localhost:8080
```

Machine is this computer's hostname (`os.Hostname`). `Since` is the local
time `15:04`, then the time since, rounded down:

| Seconds since | Text |
| --- | --- |
| under 60 | `less than a minute` |
| under 3600 | `1 minute`, `N minutes` |
| under 86400 | `1 hour`, `N hours` |
| otherwise | `1 day`, `N days` |

Exit `0` only when connected. For the rejected lines, `<server>` comes from
the secret store entry. For `○ Reconnecting to <server>` too, when the
daemon's answer has an empty `server`, `<server>` comes from the secret store
entry that step 1 read, so it is never blank (added during fix 1).

While a try is under way (`next_try_in_seconds` is `0`), the second line is
`  Try <n>, trying now` instead of `  Try <n>, next in <s> seconds`
(`1 second` when `<s>` is 1, decided by the user during the slice review). Before
any try has failed (no last error), the `Last error:` line is left out.
`TestStatusOutputs` covers both.

If the daemon answers `not_connecting` without a stop reason (a computer
paired in slice 1 whose daemon was started by hand), print
`○ Not connecting.` then `  Connect with: tervi connect`, exit `1`.

### `tervi logs` (R32)

Run `journalctl --user -u tervi -o cat --no-pager`, adding `-f` for
`tervi logs -f`, through `Runner.Stream` with standard output, until it
ends or the context ends (Ctrl+C). Exit `0`; `1` with
`✗ Can't read the log: <reason>` if `journalctl` fails to start. If
`journalctl` starts but exits with an error, it has already printed its own
message: exit `1` with no extra line (added during task 05 review). The
output is passed through unchanged, line by line as it arrives; `tervi logs`
never reads the secret store. Anything else after `logs` is wrong usage
(exit `2`).

## Boundaries

- May create or change: `internal/worker/service/`, `internal/worker/cli/`,
  `cmd/tervi/main.go`, their tests.
- Must not change: `internal/worker/daemon/`, `internal/worker/control/`
  (report a missing piece instead), `internal/worker/pair/`,
  `internal/server/`, `api/`, `design/`, `slices/` except this task's row in
  `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestInstalledCheck` — a program inside `os.TempDir()`, and one inside a `go-build` folder elsewhere, get the message and exit `1`; one in neither passes | R28 |
| `TestConnectControlError` — the service starts but the call fails: the two lines, exit `1` | R4 |
| `TestConnectCtrlC` — Ctrl+C while waiting: the message, exit `130`, and the fake daemon is still running | R4, R31 |
| `TestServiceFileContents` — exactly the data model's file, with the absolute program path | R5, R6 |
| `TestServiceFileRewrittenWhenPathChanges` — a different `ExecStart` is replaced and `daemon-reload`, `enable`, `restart` run in that order; the same path is left alone and `enable --now` runs | R5 |
| `TestEnsureRunningSteps` — the `systemctl` calls in order, then waiting for the socket | R5 |
| `TestEnsureRunningReportsStoppedDaemonStarted` — with the service file current and no daemon answering, `EnsureRunning` runs `enable --now` and reports that it started the daemon (added in fix 3) | R4 |
| `TestEnsureRunningFailures` — `systemctl` failing (on the rewrite path, and `enable --now` with a current service file, added in fix 3), systemd missing, and no socket after 10 s each give the reason | R29 |
| `TestConnectCommandResults` — every row of the result table, with output and exit code | R4 |
| `TestConnectFromDifferentInstallPrintsConnected` — the service file names another path, so it is rewritten and the daemon restarted; an `already_connected` answer then prints `✓ Connected.`, exit `0` (added in fix 2; the same-path case, `Already connected.`, is the `already connected` row of `TestConnectCommandResults`) | R4, R5 |
| `TestConnectNotPaired` | R4 |
| `TestConnectStartsStoppedService` — with no daemon answering, `connect` runs the start steps, then connects; and when the started daemon answers `already_connected` (it connected first), the output is still `✓ Connected.`, exit `0` (added in fix 3) | R4 |
| `TestConnectServiceFails` — the two lines, exit `1`, `state.json` and the secret store entry unchanged | R29 |
| `TestConnectReturnsAfterLastLine` — the command returns after printing; the fake daemon is still running | R31 |
| `TestStatusOutputs` — each of the five R16 cases, the slice-1 case, a daemon answering `not_paired`, a broken answer and an unknown `state` (the one `Can't reach` line each), and Ctrl+C while waiting (no output, exit `130`), with exit codes | R16, R17 |
| `TestNoRuntimeFolder` — extended: `tervi status` and `tervi connect` without `XDG_RUNTIME_DIR` print the runtime-folder line, exit `1`, and never call the control client or `systemctl` | R4, R16 |
| `TestStatusSince` — each `Since` wording at 0, 59, 60, 119, 120, 3599, 3600, 86399, 86400 seconds | R16 |
| `TestLogsCommand` — the exact `journalctl` arguments with and without `-f`, passed to `Runner.Stream`; the output equals the stream, and a line is shown as soon as it arrives; the failure message when `journalctl` cannot start; exit `1` with no extra line when it exits with an error; wrong usage | R32 |
| `TestNoCredentialPrinted` — no command's output contains the credential | R25 |
| `TestPairingUnreadable` — for `connect` and `status`: an unreadable store, a damaged entry, and an unreadable `state.json` each print slice 1's message and exit `1`, with no `systemctl` call and no file written | R4, R16 |
| `TestServiceDirRequired` — an empty `ServiceDir` makes `EnsureRunning` fail without writing anywhere; every test in this package uses `t.TempDir()` | R5 |
| `TestMissingInternalSetting` — a nil `Runner`, control client, empty program path, or empty `ServiceDir` prints the internal-setting line with the field name and exits `1` | — (guards the tests against real systemd) |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- Changes to `tervi pair` (task 06).
- The end-to-end test (task 07).

## If anything is unclear

Stop and report the question. Do not guess.

## Decision during building: the credential file and linger (fix 4)

Decided by the user while trying the slice: GNOME Keyring cannot be opened
over SSH, so the user could not pair or run the checklist there. The
credential moves from the OS secret store to a private file, and pairing
turns on linger. This section is the full instruction for fix 4
(branch `fix/0002-4`). It touches code that slice 1 and tasks 03 and 06
built; their task files point here.

### Requirements (copied from the spec)

- R2. While `connect` is true, the daemon shall stay connected to the server,
  and reconnect whenever the connection is lost, with no limit on the number
  of tries.
- R5. When pairing succeeds, the worker shall set up the daemon as a systemd
  user service named `tervi`, start it, and enable it so that it starts every
  time the computer starts, also when the user is not logged in: the worker
  turns on systemd's "linger" for the user's account
  (`loginctl enable-linger`). The service shall run the same `tervi` program
  file that set it up.
- R8. The daemon shall write one log line, with the time, each time the
  connection changes (connected, lost with the reason, each reconnect try with
  the reason it failed and the wait before the next), and nothing for the
  regular checks. The log is kept by systemd's journal.
- R10. When pairing succeeds, the terminal shall show `✓ Paired successfully.`,
  then `✓ tervi now runs in the background and starts when the computer starts.`,
  then `✓ Connected.` once the daemon is connected.
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`.
- R29. If the background service cannot be set up or started (for example,
  systemd is not available, or linger cannot be turned on), then the worker
  shall say so with the reason, keep the pairing, and print
  `Try again with: tervi connect`.
- R33. When `tervi pair` runs on a computer whose credential was rejected
  (R18), the worker shall print
  `The server no longer accepts this computer's credential. Starting a new pairing.`,
  remove the old pairing (`credential.json` and the pairing in
  `state.json`), and start a new pairing. On a paired computer whose
  credential was not rejected, `tervi pair` shall still refuse, as in slice 1
  (R2 of slice 1).
- R35. The worker shall keep the credential, with the server address, only
  in `~/.config/tervi/credential.json`, a file only the user's own account
  can read or write (mode `0600`, in a folder with mode `0700`). This
  replaces slice 1's OS secret store (slice 1's R3 and R18), so pairing and
  the daemon work the same over SSH, on a computer without a desktop, and
  before anyone logs in. If `tervi pair` cannot write that file, it shall
  print `✗ Can't write <path>.` and stop, before contacting the server.
- R36. If `credential.json` can be read or written by any other account
  (any permission for group or others), or belongs to another account, then the
  worker shall not use it and never change its permissions itself.
  `tervi pair`, `tervi status`, and `tervi connect` shall print
  `✗ Other users may be able to read <path>, so tervi won't use it.` then
  `Make it private with: chmod 600 <path>`, and exit with `1`. The daemon
  shall count each try as failed, with the last error
  `other users may be able to read credential.json`, and keep trying (R2),
  so it connects once the file is private again.

### What changes

**1. A file backend in `internal/worker/credential`** (new file `file.go`;
`keyring.go` is deleted).

- `NewFileBackend(dir string) Backend`, where `dir` is the worker's state
  folder (`cli.Env.StateDir`, `~/.config/tervi`). The user name `worker`
  is stored in `<dir>/credential.json`, and `worker-check` (used by
  `Store.CheckContext`) in `<dir>/credential-check.json`. Any other user
  name is an error.
- `Path(dir string) string` returns `<dir>/credential.json`; the messages
  below use it.
- `Set`: create the folder with mode `0700` if it is missing (as
  `state.WriteState` does); write the value to a temporary file in the same
  folder with mode `0600`, sync it, and rename it over the target, so a
  crash never leaves a half-written file.
- `Get`: a missing file returns `ErrNotFound`. Otherwise open the file and
  check the opened file (not the path a second time). Its permission bits
  must have nothing for group or others (`mode & 0o077 == 0`), and its
  owner must be this process's user; a failed permission or owner check
  returns the new exported error `ErrUnprotected` ("credential file is not
  private"). Something that is not a regular file (a folder, for example)
  is a plain read error, not `ErrUnprotected`, so it shows
  `✗ Can't read <path>.`. Never change the file's mode.
- The owner check compares with `os.Getuid()`. `NewFileBackend(dir)` calls
  an unexported `newFileBackend(dir string, uid int) Backend` with
  `os.Getuid()`; tests in package `credential` call `newFileBackend` with
  another user ID to pretend the file belongs to someone else.
- `Delete`: remove the file; a missing file is success.
- `Store`, `Backend`, `Entry`, `Paired`, `ErrNotFound`, `ErrDamaged`, and
  `ErrStoreUnreadable` keep their names, so callers change little. Their
  comments and internal error texts may say "credential file" instead of
  "secret store".
- `Paired` passes `ErrUnprotected` through as itself (`errors.Is(err,
  ErrUnprotected)` is true), not as `ErrStoreUnreadable`.
- `MemoryBackend` stays for tests.
- Remove `github.com/zalando/go-keyring` from `go.mod` and `go.sum`
  (`go mod tidy`). Add no library.

**2. `cmd/tervi/main.go`**: the store is
`credential.NewStore(credential.NewFileBackend(<the same folder as StateDir>))`.

**3. The messages.** `<path>` is `credential.Path(env.StateDir)`, the full
path. Exit codes stay as they are (`1`).

| Where | Now | Becomes |
| --- | --- | --- |
| `tervi pair`, the check before contacting the server (`printUnusableStore`) | `✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.` + hint | `✗ Can't write <path>.` |
| The entry cannot be read (`printStoreReadFailure`; `tervi pair`, `tervi status`, `tervi connect`) | `✗ Can't read the secret store entry.` + hint | `✗ Can't read <path>.` |
| The entry cannot be deleted (`printStoreDeleteFailure`, and `cleanupAndReport` in `internal/worker/pair/confirm.go`) | `✗ Can't delete the secret store entry.` + hint | `✗ Can't delete <path>.` |
| A failed save cannot clean up (`saveFailed` in `confirm.go`) | `✗ Couldn't remove the partly saved secret store entry.` + hint | `✗ Couldn't remove the partly saved <path>.` then `  Delete it, then run the same command again.` |
| `tervi pair` finds a damaged entry | `Found a damaged secret store entry for tervi.` | `Found a damaged <path>.` |
| `tervi pair` finds only the entry | `Found incomplete pairing data: the secret store entry exists, but state.json is missing.` | `Found incomplete pairing data: credential.json exists, but state.json is missing.` |
| `tervi pair` finds only `state.json` | `Found incomplete pairing data: state.json exists, but the secret store entry is missing.` | `Found incomplete pairing data: state.json exists, but credential.json is missing.` |
| Any read that returns `ErrUnprotected` (`tervi pair` at its start, `tervi status`, `tervi connect`) | — | `✗ Other users may be able to read <path>, so tervi won't use it.` then `  Make it private with: chmod 600 <path>`; exit `1`, before any server request or `systemctl` call |
| After pairing (`backgroundStartedMessage` in `handover.go`) | `✓ tervi now runs in the background and starts when you log in.` | `✓ tervi now runs in the background and starts when the computer starts.` |

Every hint line `Make sure you are logged in to a desktop session and the
keyring is unlocked…` is removed. Every other message stays as it is.

**4. The daemon's error texts** (`internal/worker/daemon/errors.go`, task
03's "Error texts" table). The last error, and the log line with its first
letter in capitals:

| Cause | Last error |
| --- | --- |
| `ErrNotFound` | `credential.json is missing` |
| `ErrDamaged` | `credential.json is damaged` |
| `ErrUnprotected` (checked before `ErrStoreUnreadable`) | `other users may be able to read credential.json` |
| `ErrStoreUnreadable` | `can't read credential.json` |

An `ErrUnprotected` try is a failed try like any other: `connect` stays
true, the wait grows as in R9, and the file is read again on the next try.

**5. Linger in `internal/worker/service`.** `EnsureRunning` runs
`loginctl enable-linger` (no other arguments) through `Runner.Run` once, as
its first command, before any `systemctl` call, on both paths (the rewrite
path and `enable --now`). It changes nothing when linger is already on. If
it fails, `EnsureRunning` returns the command's trimmed output as the
reason (or the error, when there is no output), as `runSystemctl` does, and
runs no `systemctl` call; the callers then print R29's lines as they do
today. The service file does not change.

**6. The end-to-end test.** Both worker fixtures in `test/e2e` (the
connection fixture in `connection_helpers_test.go` and the pairing fixture
in `pairing_test.go`) use `credential.NewFileBackend` on the test's
temporary state folder instead of the in-memory store, so the real file is
created, read, and deleted.
`TestNoSecretsEndToEnd` also checks that `credential.json` has mode `0600`,
and that the credential appears in no other file in the state folder or
the service folder.

### Boundaries (fix 4)

- May create or change: `internal/worker/credential/`,
  `internal/worker/cli/`, `internal/worker/pair/` (the messages above
  only), `internal/worker/daemon/errors.go`,
  `internal/worker/daemon/daemon_test.go`, `internal/worker/service/`,
  `cmd/tervi/main.go`, `test/e2e/`, `go.mod`, `go.sum`, and their tests.
  Existing tests may change to expect the new messages, the extra
  `loginctl` call, and the file backend.
- Must not change: `internal/server/`, `internal/worker/control/`,
  `internal/worker/state/`, the rest of `internal/worker/daemon/`, `api/`,
  `design/`, `slices/`.

### Definition of done (fix 4)

| Test | Proves |
| --- | --- |
| `TestFileBackendSavesPrivateFile` — `Set` creates a missing folder with mode `0700` and writes `credential.json` with mode `0600`; `Get` returns the value; no temporary file is left; `Store.CheckContext` leaves no `credential-check.json` | R35 |
| `TestFileBackendMissingIsNotFound` — `Get` of a missing file is `ErrNotFound`; `Delete` of a missing file succeeds | R35 |
| `TestFileBackendRefusesOpenPermissions` — modes `0640`, `0604`, `0660`, and `0644` each give `ErrUnprotected`, and the mode is unchanged afterwards | R36 |
| `TestFileBackendRefusesOtherOwner` — a backend using another user ID gives `ErrUnprotected` | R36 |
| `TestPairUnwritableCredentialStopsBeforeServer` — with an existing state folder of mode `0500` holding no `state.json` and no `credential.json` (and the real file backend), `tervi pair` prints `✗ Can't write <path>.`, exits `1`, and sends no request to the server; skipped when the test runs as root, which ignores the mode | R35 |
| `TestCredentialFileMessages` — in `internal/worker/cli`: each message in the table above that `cli` prints, with the full path; in `internal/worker/pair` (a test of the same name there): the two `confirm.go` messages. No keyring hint anywhere in the worker's output | R35 |
| `TestUnprotectedCredentialMessages` — with a `0644` `credential.json`, `tervi pair`, `tervi status`, and `tervi connect` each print the two lines and exit `1`, with no server request and no `systemctl` call; the mode is still `0644` | R36 |
| `TestUnprotectedCredentialIsFailedTry` — the daemon with a real file backend and a `0644` file: the try fails with the last error above and its log line, `connect` stays true, and after the test sets `0600` the next try connects | R36, R2 |
| `TestErrorTexts` — extended with the four texts above | R8, R36 |
| `TestEnsureRunningTurnsOnLinger` — `loginctl enable-linger` runs once, before any `systemctl` call, on the rewrite path and on the `enable --now` path; when it fails, its output is the reason and no `systemctl` call runs | R5, R29 |
| `TestPairHandsOver` — updated: the R10 line with `starts when the computer starts.` | R10 |
| `TestNoSecretsEndToEnd` — extended as in "The end-to-end test" | R25, R35 |

`go vet ./...` and `go test ./...` pass, and `go.mod` no longer lists
`github.com/zalando/go-keyring`.
