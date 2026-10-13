# Task 04 — Worker: the daemon's socket, its lock, and `tervi daemon`

Slice: 0002-connection    Risk: core    Depends on: 03

## Goal

Make the daemon a program: `tervi daemon` takes a per-user lock so only one
runs, opens a private Unix socket that other `tervi` commands use to ask for
its status and tell it to connect, runs the engine from task 03, and stops
cleanly on Ctrl+C or when systemd stops it.

## Requirements (copied from the spec)

- R3. While `connect` is false, the daemon shall keep running but not try to
  connect. It shall still answer `tervi status` and `tervi connect`. *This
  task: answering over the socket.*
- R7. At most one daemon shall run per user account. A second one shall exit
  at once and leave the first one running.
- R8. The daemon shall write one log line, with the time, each time the
  connection changes (connected, lost with the reason, each reconnect try with
  the reason it failed and the wait before the next), and nothing for the
  regular checks. The log is kept by systemd's journal. *This task: the lines
  go to standard output, which systemd sends to the journal.*
- R26. Only the user's own account shall be able to talk to the daemon.
- R30. When the user runs `tervi daemon`, the worker shall run the daemon in
  that terminal and print its log lines there, until the user presses Ctrl+C.
  If a daemon is already running for this user account, it shall print
  `tervi is already running in the background.` and exit.

## Context

### What exists (task 03)

`internal/worker/daemon`: `New(Options)`, `Run(ctx)`, `Status()`,
`Connect(ctx)`, with the `Status` and `ConnectResult` types. The engine
writes its log lines to `Options.Log`.

### Paths

The runtime folder is `$XDG_RUNTIME_DIR/tervi`, created with mode `0700`.
It holds `daemon.lock` and `daemon.sock`. If `XDG_RUNTIME_DIR` is empty,
`tervi daemon` prints
`✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).`
and exits with `1`. Put the path logic in one exported function, used by the
commands in task 05.

### The lock (R7)

`internal/worker/control` opens `daemon.lock`, creating it with mode `0600`
if it is missing (as in `design/data-model.md`), takes an exclusive,
non-blocking `flock` (`syscall.Flock` with `LOCK_EX|LOCK_NB`) on it, and
keeps the file open while the daemon runs. Linux releases it when the process ends. If the
lock is taken: print `tervi is already running in the background.` and exit
with `0`. (Exit `0` so systemd, which restarts only after a failure, does not
keep starting the service while the user runs one by hand.)

### The socket (R26)

After taking the lock: remove any old `daemon.sock` (a crash can leave one),
listen on it, and set its mode to `0600`. For every accepted connection, read
the other program's user id with `syscall.GetsockoptUcred(fd,
syscall.SOL_SOCKET, syscall.SO_PEERCRED)` and close the connection at once
unless it equals `os.Getuid()`. Put the check behind a small function that
tests can call with a chosen user id.

Serve HTTP over the socket with `net/http`:

| Request | Answer (JSON) |
| --- | --- |
| `GET /status` | `{"state": "connected", "server": "…", "since": "<RFC 3339>"}`, `{"state": "reconnecting", "server": "…", "try": 3, "next_try_in_seconds": 4, "last_error": "…"}`, `{"state": "not_connecting", "stop_reason": "credential_rejected"}` (or `null`), or `{"state": "not_paired"}` |
| `POST /connect` | `{"result": "connected"}`, `already_connected`, `rejected`, `not_paired`, or `{"result": "still_trying", "last_error": "…"}` — from `Daemon.Connect` |

`next_try_in_seconds` is rounded to the nearest whole second, at least 0.
`last_error` is `null` before any try has failed (see task 03's `Status`).

### The client

In `internal/worker/control`, a client used by the commands:
`Status(ctx) (StatusAnswer, error)` and `Connect(ctx) (ConnectAnswer, error)`.
When the socket file is missing or nothing answers, they return a
recognisable `ErrNotRunning`. `Status` allows 5 seconds for the answer and
returns `ErrNotRunning` when none comes (a frozen daemon must not make
`tervi status` hang). `Connect` allows up to 35 seconds for the answer.

### `tervi daemon` (R30)

Add the command to `internal/worker/cli`. Split `cli.go` by topic if it would
pass 300 lines: one file per command. The usage text becomes:

```text
Usage: tervi pair --server <url>
       tervi connect
       tervi status
       tervi logs [-f]
       tervi daemon
```

`connect`, `status`, and `logs` are added to the usage now but built in task
05; until then they print `Not available yet.` and exit with `1`. `daemon`
takes no flags; anything after it is wrong usage (exit `2`, as in slice 1).

`tervi daemon` takes the lock, opens the socket, and runs the engine with its
log going to standard output, until the context ends. Then it closes the
socket, removes `daemon.sock`, releases the lock, and exits with `0`. An
unexpected failure exits with `1`.

In `cmd/tervi/main.go`, stop on `SIGTERM` as well as `os.Interrupt`
(systemd sends `SIGTERM`), and pass what the daemon needs (state folder,
secret store) through `cli.Env` or a similar existing pattern.

## Boundaries

- May create or change: `internal/worker/control/`, `internal/worker/cli/`,
  `cmd/tervi/main.go`, their tests.
- Must not change: `internal/worker/daemon/` (report a missing piece
  instead), `internal/worker/pair/`, `internal/worker/state/`,
  `internal/server/`, `internal/protocol/`, `api/`, `design/`, `slices/`
  except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestSecondDaemonExits` — a second `tervi daemon` with the same runtime folder prints the message, exits `0`, and the first keeps answering | R7, R30 |
| `TestLockReleasedWhenDaemonEnds` — after the first ends, a new one starts | R7 |
| `TestOldSocketFileReplaced` — a leftover `daemon.sock` does not stop a new daemon | R7 |
| `TestRuntimeFolderPrivate` — the folder is `0700`, the socket and the lock file `0600` | R26 |
| `TestOtherUserRefused` — the peer check refuses a different user id and accepts the same one | R26 |
| `TestStatusOverSocket` — each state's JSON | R3 |
| `TestConnectOverSocket` — each result's JSON | R3 |
| `TestClientNotRunning` — missing socket, refused socket, and a daemon that does not answer `Status` within 5 s give `ErrNotRunning` | R16 support |
| `TestDaemonCommandStops` — cancelling the context makes `tervi daemon` exit `0` and remove the socket | R30 |
| `TestDaemonPrintsLogLines` — the engine's lines appear on standard output | R8, R30 |
| `TestNoRuntimeFolder` — the message and exit `1` | R30 |
| `TestUsage` — the usage text lists every command of this slice, and wrong usage of `daemon` prints it and exits `2` (purpose: the user can discover the commands) | usage |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- systemd, `tervi connect`, `tervi status`, `tervi logs` (task 05).
- Changes to `tervi pair` (task 06).

## If anything is unclear

Stop and report the question. Do not guess.
