# Task 05 — Worker: `tervi disconnect`

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 04

## Goal

Add `tervi disconnect`. With a daemon running, it asks the daemon over the
socket (`POST /disconnect`), which closes the connection with `1000`, writes
`connect: false`, and stays running. With no daemon, the command takes
`daemon.lock` itself and writes `connect: false`, without starting
anything. Either way the computer stays disconnected, also after a reboot,
until `tervi connect`.

## Requirements (copied from the spec)

- R1. When the user runs `tervi disconnect` on a paired computer that is
  connected or reconnecting, the worker shall close the connection, stop
  trying to connect, keep the pairing, and print
  `✓ Disconnected. tervi stays in the background but won't connect.` then
  `Connect again with: tervi connect`, and exit with `0`. The daemon shall
  keep running, and write the log line `○ Disconnected.`
- R2. While the user has disconnected, the daemon shall not connect, also
  after the computer restarts, until the user runs `tervi connect`.
- R3. When `tervi disconnect` runs and tervi is not running in the
  background, the worker shall still record that it must not connect, print
  `✓ Disconnected. tervi won't connect, including after the computer restarts.`
  then `Connect again with: tervi connect`, and exit with `0`. It shall not
  start tervi in the background.
- R4. When `tervi disconnect` runs on a computer the user already
  disconnected, the worker shall print `Already disconnected.`, change
  nothing, and exit with `0`.
- R5. When `tervi disconnect` runs on a computer whose credential was
  rejected (slice 2's R18), the worker shall change nothing, print
  `Not connecting: the server doesn't accept this computer's credential.`
  then `Pair again with: tervi pair --server <server>`, and exit with `0`.
- R6. When `tervi disconnect` runs on a computer that is not paired, the
  worker shall print
  `This computer isn't paired. Pair it with: tervi pair --server <url>`,
  change nothing, and exit with `1`.
- R7. While the user has disconnected, `tervi status` shall show
  `○ Disconnected.` then `  Connect with: tervi connect`, and exit with `1`,
  also when tervi is not running in the background.
  *This task: proving it after `tervi disconnect`, with and without a
  daemon. The message itself is task 04.*
- R8. While the user has disconnected, the Machines page shall show the
  computer as `Offline · last seen <time ago>`, as for any computer that is
  not connected.
  *This task: the daemon closes with `1000`; the server already shows a
  closed machine Offline (slice 2).*
- R9. `tervi connect` after a disconnect shall connect again, as in slice 2's
  R4.
- R21. On such a computer, `tervi status`, `tervi connect`, and
  `tervi disconnect` shall print `✗ This computer was unpaired on the server.`
  then `Pair it again with: tervi pair --server <server>`. `tervi status` and
  `tervi connect` shall exit with `1`; `tervi disconnect` with `0`, as in R5.
  *This task: `tervi disconnect` on such a computer.*

## Context

### What exists (task 04 and slice 2)

- `internal/worker/control` runs the socket (`GET /status`,
  `POST /connect`) and holds `daemon.lock` with `flock` for the daemon's
  life (`acquireLock` in `control/daemon.go`, which answers
  `ErrAlreadyRunning` when another holder has it).
- The daemon is idle while `connect` is false, and reads `state.json` when
  it starts, so a daemon that starts after a disconnect stays idle (R2).
- `state.WriteState` replaces `state.json` atomically.
- Stop reasons: `credential_rejected`, `credential_revoked` (task 04).

### The lock without a daemon: `internal/worker/control/lock.go` (new)

Move `acquireLock` here from `daemon.go` unchanged, and add:

```go
// WithLock takes daemon.lock without waiting and runs change while holding
// it. It returns ErrAlreadyRunning, without running change, when a daemon
// (or another holder) has the lock.
func WithLock(runtimeDir string, change func() error) error
```

It creates the runtime folder as the daemon does if it is missing. Task 06
also uses it. This is the plan's "One writer" rule: only the lock holder
writes `connect` and `stop_reason`.

### The daemon: `internal/worker/daemon/disconnect.go` (new)

```go
// Disconnect stops connecting: it writes connect: false, closes an open
// connection with 1000, and stops any wait for a reconnect.
func (d *Daemon) Disconnect(ctx context.Context) DisconnectResult
```

| Daemon state | Does | Result |
| --- | --- | --- |
| connected, or reconnecting | writes `connect: false`, `stop_reason: null`; closes the connection with `1000` (`daemon disconnected`) or stops the wait; status `not_connecting`, no reason; logs `○ Disconnected.` | `disconnected` |
| `not_connecting`, no reason | nothing | `already_disconnected` |
| `not_connecting`, `credential_rejected` | nothing | `rejected` |
| `not_connecting`, `credential_revoked` | nothing | `revoked` |
| `not_paired` | nothing | `not_paired` |
| writing `state.json` fails | nothing else changes | `failed`, with the path |

`Disconnect` and `Connect` never run at the same time (they share
`connectMu`). A `Connect` can hold it for up to 30 seconds while it waits
for a connection, so `Disconnect` waits for it, and `Client.Disconnect`'s
timeout (40 seconds) is longer than `Client.Connect`'s (35 seconds): a
`tervi disconnect` run during a waiting `tervi connect` finishes after it,
and disconnects. The close and the state change happen before the answer
is sent, so the machine is Offline on the server within the 5 seconds R1's
example allows.

### The socket: `internal/worker/control`

`POST /disconnect` calls the engine's `Disconnect` and answers
`{"result": "<result>"}`, plus `"path"` for `failed`, with the same
encoding pattern as `ConnectAnswer`. `Client.Disconnect(ctx)` calls it with
a 40-second timeout (above).

### The command: `internal/worker/cli/disconnect.go` (new)

`tervi disconnect`, no arguments (anything else prints the usage line and
exits with `2`). Steps:

1. Read the pairing (`credential.Paired`). Not paired: print
   `This computer isn't paired. Pair it with: tervi pair --server <url>`
   and exit `1` (R6). Unreadable files: as `tervi connect` reports them.
2. `control.WithLock(runtimeDir, …)`:
   - **Got the lock (no daemon):** read `state.json`. If `stop_reason` is
     `credential_revoked` or `credential_rejected`, or `connect` is already
     false with no reason, change nothing (see the table). Otherwise write
     `connect: false`, `stop_reason: null`, keeping every other field.
   - **`ErrAlreadyRunning`:** call `Client.Disconnect`.
3. Print and exit:

| Outcome | Prints | Exit |
| --- | --- | --- |
| Daemon answered `disconnected` | `✓ Disconnected. tervi stays in the background but won't connect.` / `  Connect again with: tervi connect` | `0` |
| Wrote it with no daemon | `✓ Disconnected. tervi won't connect, including after the computer restarts.` / `  Connect again with: tervi connect` | `0` |
| Already disconnected (either way) | `Already disconnected.` | `0` |
| Rejected (either way) | `Not connecting: the server doesn't accept this computer's credential.` / `  Pair again with: tervi pair --server <server>` | `0` |
| Revoked (either way) | `✗ This computer was unpaired on the server.` / `  Pair it again with: tervi pair --server <server>` | `0` |
| `failed` or a write failure | `✗ Can't write <path>.`, as `tervi connect` prints it in slice 2 | `1` |
| The daemon answered `not_paired` | `This computer isn't paired. Pair it with: tervi pair --server <url>` (R6) | `1` |
| The socket failed after the lock was busy | as `tervi status` reports a socket failure | `1` |

The hint lines are indented two spaces, as in slice 2 (fix 6).
`tervi disconnect` never starts the service. Add `tervi disconnect` to the
usage line.

## Boundaries

- May create or change: `internal/worker/control/`,
  `internal/worker/daemon/`, `internal/worker/cli/` (the new file, the
  dispatch in `cli.go`, the usage line), `cmd/tervi/` (wiring only), their
  tests.
- Must not change: `internal/worker/state/` (use it),
  `internal/worker/service/`, `internal/worker/pair/`, anything under
  `internal/server/`, `api/`, `design/`, `slices/` except this task's row in
  `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestDisconnectConnected` — a connected daemon closes with `1000`, writes `connect: false`, keeps running, answers `disconnected`; the command prints R1's lines and exits `0` | R1, R8 |
| `TestDisconnectReconnecting` — a daemon waiting for its next try stops waiting and makes no more tries | R1 |
| `TestDisconnectNoDaemon` — with no daemon, the command takes the lock, writes `connect: false` keeping the other fields, starts nothing, prints R3's lines, exits `0` | R3 |
| `TestDisconnectedAfterRestart` — a daemon started after a disconnect stays idle and makes no tries | R2 |
| `TestDisconnectAlready` — both ways, `Already disconnected.`, nothing written, exit `0` | R4 |
| `TestDisconnectRejectedOrRevoked` — both ways, the matching two lines, nothing written, exit `0` | R5, R21 |
| `TestDisconnectNotPaired` — the not-paired line, exit `1` | R6 |
| `TestDisconnectThenStatus` — `tervi status` shows `○ Disconnected.` with the daemon, and with no daemon | R7 |
| `TestConnectAfterDisconnect` — `tervi connect` connects again | R9 |
| `TestWithLockBusy` — while a daemon holds the lock, `WithLock` returns `ErrAlreadyRunning` and does not run the change; a daemon cannot start while `WithLock` runs | R1, R3 |
| `TestDisconnectWriteFailure` — a `state.json` that cannot be written prints the path and exits `1`, both ways | R1, R3 |
| `TestDisconnectDuringConnect` — a `tervi disconnect` sent while a `POST /connect` waits for an unreachable server answers `disconnected` once the connect ends, and no try follows | R1 |
| `TestDisconnectLogLine` — the daemon writes `○ Disconnected.` once | R1 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- `tervi unpair` (task 06).
- The end-to-end test with the real server (task 08).

## If anything is unclear

Stop and report the question. Do not guess.
