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

**The service file.** `~/.config/systemd/user/tervi.service` (use
`os.UserConfigDir()`), exactly the contents in `design/data-model.md`, with
`ExecStart=<absolute program path> daemon`. Create the folder if needed.

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
`EnsureRunning(ctx) error` doing steps 1–2. Put whatever the commands need
from systemd (the `Runner`), the program path, and the control client into
`cli.Env`, so tests can replace them. When a field is nil, `cmd/tervi`'s
real value is used only if `main` sets it; tests set fakes.

Put the printing of the shared messages (the two R29 lines, the two
rejected lines, the not-paired line, the two control-error lines) in
functions in `internal/worker/cli`, so task 06 reuses them.

### `tervi connect` (R4, R28, R29, R31)

1. Installed check (R28).
2. Not paired → `This computer isn't paired. Pair it with: tervi pair --server <url>`, exit `1`.
3. `EnsureRunning` → on failure, the two lines above, exit `1`. The pairing
   is untouched.
4. `control.Client.Connect` and print:

| Result | Output | Exit |
| --- | --- | --- |
| `connected` | `✓ Connected.` | `0` |
| `already_connected` | `Already connected.` | `0` |
| `still_trying` | `Not connected yet: <last error>. tervi keeps trying in the background.` then `Check with: tervi status` | `1` |
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

1. Not paired → `✗ This computer isn't paired.` then
   `  Pair it with: tervi pair --server <url>` (`<url>` printed as is),
   exit `1`. A daemon that answers `not_paired` gets the same two lines.
2. `control.Client.Status`; `ErrNotRunning` →
   `✗ tervi isn't running in the background.` then
   `  Start it with: tervi connect`, exit `1`.
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
the secret store entry.

**Open — waiting for the user's wording:** what `○ Reconnecting` shows while
a try is under way (`next_try_in_seconds` is `0`) and before any try has
failed (no last error). Until the user answers, build the two cases so the
wording is one string each, easy to change.

If the daemon answers `not_connecting` without a stop reason (a computer
paired in slice 1 whose daemon was started by hand), print
`○ Not connecting.` then `  Connect with: tervi connect`, exit `1`.

### `tervi logs` (R32)

Run `journalctl --user -u tervi -o cat --no-pager`, adding `-f` for
`tervi logs -f`, through `Runner.Stream` with standard output, until it
ends or the context ends (Ctrl+C). Exit `0`; `1` with
`✗ Can't read the log: <reason>` if `journalctl` fails to start. Anything
else after `logs` is wrong usage (exit `2`).

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
| `TestEnsureRunningFailures` — `systemctl` failing, systemd missing, and no socket after 10 s each give the reason | R29 |
| `TestConnectCommandResults` — every row of the result table, with output and exit code | R4 |
| `TestConnectNotPaired` | R4 |
| `TestConnectStartsStoppedService` — with no daemon answering, `connect` runs the start steps, then connects | R4 |
| `TestConnectServiceFails` — the two lines, exit `1`, `state.json` and the secret store entry unchanged | R29 |
| `TestConnectReturnsAfterLastLine` — the command returns after printing; the fake daemon is still running | R31 |
| `TestStatusOutputs` — each of the five R16 cases, the slice-1 case, and a daemon answering `not_paired`, with exit codes | R16, R17 |
| `TestStatusSince` — each `Since` wording at 0, 59, 60, 119, 120, 3599, 3600, 86399, 86400 seconds | R16 |
| `TestLogsCommand` — the exact `journalctl` arguments with and without `-f`, passed to `Runner.Stream`; failure message; wrong usage | R32 |
| `TestNoCredentialPrinted` — no command's output contains the credential | R25 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- Changes to `tervi pair` (task 06).
- The end-to-end test (task 07).

## If anything is unclear

Stop and report the question. Do not guess.
