# Task 03 — Worker: the daemon's connection

Slice: 0002-connection    Risk: core    Depends on: 01

## Goal

Build the daemon's core as a Go package that runs in one process: it obeys
`connect` in `state.json`, opens the connection, sends the checks, notices a
dead server, reconnects with growing random waits, stops on a rejected
credential, and writes one log line per connection change. Commands and the
background service come in later tasks; this task is the engine they drive.

## Requirements (copied from the spec)

- R2. While `connect` is true, the daemon shall stay connected to the server,
  and reconnect whenever the connection is lost, with no limit on the number
  of tries.
- R3. While `connect` is false, the daemon shall keep running but not try to
  connect. It shall still answer `tervi status` and `tervi connect`. *This
  task: idle while false, and the `Status` and `Connect` methods.*
- R8. The daemon shall write one log line, with the time, each time the
  connection changes (connected, lost with the reason, each reconnect try with
  the reason it failed and the wait before the next), and nothing for the
  regular checks. The log is kept by systemd's journal. *This task: the
  lines, written to the daemon's output.*
- R9. Between reconnect tries, the daemon shall wait about 1, 2, 4, 8 ...
  seconds, at most about 30 seconds, then keep trying about every 30
  seconds. Each wait shall be that value, made longer or shorter at random by
  up to 20%, so that workers that lost the server at the same moment do not
  all try again at the same moment.
- R12. When either side closes the connection on purpose, the other side shall
  notice at once. *This task: the worker side.*
- R13. If the other side stops answering without closing (for example, the
  network is lost), then each side shall notice within the timeout: 30
  seconds by default. *This task: the worker side.*
- R14. The check interval (default 10 seconds) and the timeout (default 30
  seconds) shall be set on the server only. The server shall tell each worker
  the values when it connects, so both sides always use the same ones.
  *This task: the worker uses the values from `hello`.*
- R18. If the server answers that it does not know this computer's credential,
  then the daemon shall stop trying, set `connect` to false, save the reason in
  `state.json`, and print that the credential was rejected and how to pair
  again.
- R19. The daemon shall treat only that answer as a rejection. A refused
  connection, a timeout, a network error, or a server error shall lead to
  another try, never to `connect` false.
- R20. When `tervi connect` runs after a rejection, the daemon shall try once.
  If the credential is rejected again, it shall stop as in R18. *This task:
  the `Connect` method.*
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`. *This task: the daemon's log and
  `state.json`.*

## Context

### What exists

- `internal/protocol` (task 01): `Hello`, `Ping`, `Pong`, the type names,
  `MaxMessageSize`, the close codes, `DefaultHelloTimeout`. Read
  `api/asyncapi.yaml` for every rule of the connection.
- `internal/worker/credential`: `Store.Get()` returns the entry
  `{server, credential}` from the secret store.
- `internal/worker/state`: `ReadState`, `WriteState` (atomic replace).
- `github.com/coder/websocket` is in `go.mod`.

### `state.json`: two new fields

In `internal/worker/state`, add to `State`:

```go
Connect    bool    `json:"connect"`
StopReason *string `json:"stop_reason"`
```

A missing `connect` reads as `false`; `stop_reason` is written as `null` or
`"credential_rejected"`. Only the daemon changes these two fields (see
`design/data-model.md`): it reads `state.json`, changes them, and writes the
whole record back, keeping `machine_id`, `credential_saved`, and `confirmed`
exactly as they were.

### "Is this computer paired?": `internal/worker/credential`

Add one function used by the daemon here and by the commands in task 05:

```go
// Paired reports whether this computer is paired: state.json exists with
// confirmed true, and the secret store entry exists and is usable.
func Paired(stateDir string, store *Store) (entry Entry, record state.State, paired bool, err error)
```

`err` is set when `state.json` or the secret store cannot be read, or the
entry is damaged (`ErrDamaged`); `paired` is then false. A missing file or a
missing entry is not an error: `paired` is false and `err` is nil.

### The package: `internal/worker/daemon`

```go
type Options struct {
    StateDir string
    Store    *credential.Store
    Log      io.Writer        // one line per connection change
    Now      func() time.Time // nil: time.Now
    Rand     func() float64   // nil: math/rand/v2; returns [0,1)
    // Dial opens the WebSocket; nil uses websocket.Dial from coder/websocket.
    Dial func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}
func New(opts Options) *Daemon
func (d *Daemon) Run(ctx context.Context) error   // until ctx ends
func (d *Daemon) Status() Status
func (d *Daemon) Connect(ctx context.Context) ConnectResult
```

`Run` reads `state.json`, then loops:

- `connect` false, or no confirmed `state.json`: stay idle until `Connect`
  is called or `ctx` ends. No network at all.
- `state.json` cannot be read or parsed: stay idle the same way, write
  nothing, and answer `Status` with `not_connecting` and no stop reason.
  The daemon never writes over a `state.json` it could not read, so the
  pairing fields are never lost.
- `connect` true: try to connect, as below. Each try reads the secret store
  entry again (through `credential.Paired`). A store that cannot be read, a
  damaged entry, or a missing entry is a **failed try**: it gets the log
  line and the R9 wait like any other failure, and never sets `connect` to
  false. This matters at login: systemd may start the daemon before GNOME
  Keyring is unlocked, and the next try then succeeds.
- When `ctx` ends: close an open connection with `1000` and return `nil`.

### One try

1. Dial `<server>/api/v1/machines/current/connection`, with `http` changed to
   `ws` and `https` to `wss`, header `Authorization: Bearer <credential>`,
   within 10 seconds.
2. `401` with `{"error": "unknown_credential"}`: rejection (below). Any
   other failure, including other `401` bodies, `5xx`, a refused connection,
   a timeout, or a network error: a failed try.
3. Wait for `hello`, at most `protocol.DefaultHelloTimeout`. Then the
   connection counts as connected: log the line, reset the try count, and
   use `hello`'s interval and timeout.
4. Send `ping` every interval, counted from the previous `ping`, without
   waiting for `pong`.
5. Every message received, of any type, restarts the timeout count. Unknown
   types are ignored. When nothing arrives for the timeout, close with
   `4001` and treat it as a lost connection.
6. A close from the server, or any read error, is a lost connection.

### Waits between tries (R9)

Tries are numbered from 1. The base wait before try *n* is:

| Try | 1 | 2 | 3 | 4 | 5 | 6 and later |
| --- | --- | --- | --- | --- | --- | --- |
| Base wait before it | 1 s | 2 s | 4 s | 8 s | 16 s | 30 s |

Each actual wait is `base × (0.8 + 0.4 × Rand())`.

- **After a lost connection:** wait before try 1, then log
  `… Reconnecting (try 1)` and try.
- **The first try after `Run` starts, or after `Connect`:** it is try 1,
  with no wait and no "Reconnecting" line.
- **When try *n* fails:** log the failure line, whose "Next try in" is the
  wait chosen for try *n*+1; wait it; log `… Reconnecting (try <n+1>)`; try.
- **When `hello` arrives:** the next loss starts again from try 1.

So the first announced wait is at least 1.6 seconds and rounds to 2 or more.
There is no limit on tries.

### Rejection (R18, R20)

Stop trying. Write `connect: false`, `stop_reason: "credential_rejected"` to
`state.json`, log the rejection line, and go idle. `Connect` after a
rejection sets `connect` back to true, clears `stop_reason`, and makes tries
as usual; a second `401` stops again the same way.

### `Connect(ctx)`

Reads the pairing again (state and secret store entry), so a new pairing is
picked up. Then:

| Situation | Result |
| --- | --- |
| No confirmed `state.json`, or no secret store entry | `not_paired` |
| `state.json` cannot be read or parsed | `still_trying` with the last error `can't read state.json`; nothing is written, and the loop stays idle |
| Already connected | `already_connected` (nothing changes) |
| Otherwise (including a secret store that cannot be read, or a damaged entry) | Writes `connect: true`, `stop_reason: null`; wakes the loop; waits until connected (`connected`), rejected (`rejected`), or 30 seconds or `ctx` end (`still_trying`, with the last error, for example `can't read the secret store`; no last error if no try has failed yet) |
| Writing `connect: true` to `state.json` fails | `failed`, with the path of `state.json`. Nothing changes: the loop is not woken and stays as it was. (Decided by the user during task 03.) |

### `Status()`

| State | Fields |
| --- | --- |
| `connected` | server, connected since |
| `reconnecting` | server, try number, time of the next try, last error |
| `not_connecting` | stop reason (`credential_rejected` or none) |
| `not_paired` | — |

`reconnecting` covers every moment `connect` is true and no connection is
open, including a try that is under way and the time before any try has
failed:

- **try number:** the try under way, or the next one while waiting.
- **time of the next try:** while waiting, when the wait ends; while a try
  is under way, the current time (so `next_try_in_seconds` is `0`).
- **last error:** the most recent failure since the last connection, or
  none before any try has failed.

`server` is read from the secret store entry, never from `state.json`.
While `reconnecting`, `server` is the last server the daemon read; it is
empty if the daemon has not read one yet (for example, the keyring was
locked from the start). The daemon never answers `not_connecting` while
`connect` is true: an unknown server does not change the state (added
during fix 1; `tervi status` fills in the server, see task 05).

### Error texts

The same error has one text, written in two ways:

| Failure | `last error` (in `Status`, `Connect`, and mid-sentence) | In a log line, after `✗ ` |
| --- | --- | --- |
| Connection refused | `connection refused` | `Connection refused` |
| No answer within 10 s, or no `hello` within 30 s | `no answer from the server` | `No answer from the server` |
| An HTTP answer other than `101` and the rejection | `the server answered <status code>` | `The server answered <status code>` |
| The secret store cannot be read | `can't read the secret store` | `Can't read the secret store` |
| The secret store entry is damaged | `the secret store entry is damaged` | `The secret store entry is damaged` |
| The secret store entry is missing (`state.json` is confirmed) — decided by the user during task 03 | `the secret store entry is missing` | `The secret store entry is missing` |
| `state.json` cannot be read or parsed (only in `Connect`) | `can't read state.json` | — (never logged as a try) |
| Any other network error | Go's error text, first letter lower-case | the same, first letter upper-case |

The log form is the `last error` form with its first letter upper-case.

### Log lines (R8)

Each line starts with the local time `15:04:05` and a space. Exact texts:

| Event | Line |
| --- | --- |
| Connected | `✓ Connected to <server>` |
| Lost | `✗ Connection lost: <reason>` |
| A try starts | `… Reconnecting (try <n>)` |
| A try failed | `✗ <error>. Next try in <s> seconds` (`1 second` when `<s>` is 1, decided by the user during the slice review) |
| Rejected | `✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server <server>` |

`<reason>`: `the server is stopping` (1001), `replaced by a newer connection`
(4000), `no answer from the server for <timeout> seconds` (timeout),
`the server closed the connection` (any other close), or the network error
in its `last error` form. `<error>` is the log form from "Error texts".
`<s>` is the wait rounded to the nearest whole second; by the waits above it
is always 2 or more. Nothing is logged for `ping`, `pong`, or `hello`. The
first try after start or after `Connect` logs no "Reconnecting" line; only
tries after a loss or a failure do.

### Secrets

Never write the credential to the log, an error, `Status`, or `state.json`.

## Boundaries

- May create or change: `internal/worker/daemon/`, `internal/worker/state/`
  (the two fields and their tests), `internal/worker/credential/` (only the
  `Paired` function and its tests), their tests.
- Must not change: `internal/worker/cli/`, `internal/worker/pair/`,
  `internal/server/`, `internal/protocol/` (report a missing piece instead),
  `cmd/`, `api/`, `design/`, `slices/` except this task's row in `plan.md`.

## Definition of done

Tests use a fake server built with `httptest` and `coder/websocket`, an
injected clock and `Rand`, and short test timings.

| Test | Proves |
| --- | --- |
| `TestConnectsAndPings` — connects, sends `ping` at `hello`'s interval, counted from the previous ping | R2, R14 |
| `TestUsesServerTiming` — different `hello` values change the ping interval and the timeout | R14 |
| `TestServerCloseNoticedAtOnce` — a server close starts reconnecting without waiting for the timeout | R12 |
| `TestServerSilenceTimesOut` — no message for the timeout closes with `4001` and reconnects | R13 |
| `TestReconnectWaits` — after a loss: wait before try 1, then 2, 4, 8, 16, 30, 30 s as bases; the first try after start has no wait; the logged "Next try in" is the wait before the next try; with `Rand` 0 and almost 1 each wait is within 0.8–1.2 × base; the count resets after `hello` | R9, R8 |
| `TestStoreUnreadableIsFailedTry` — while `connect` is true, an unreadable store, a damaged entry, and a missing entry each give a failed try with its log line and wait, `connect` stays true, and the next try reads the store again and connects | R2, R5 |
| `TestStatusWhileTrying` — `Status` during a try under way (`next_try_in_seconds` 0) and before any failure (no last error) | R16 support |
| `TestStatusReconnectsWhenSecretStoreIsUnreadable`, `TestRefreshStateKeepsLastServer` — with the store unreadable, `Status` is `reconnecting` with its last error, never `not_connecting`; the last server read is kept (added in fix 1) | R2, R16 support |
| `TestErrorTexts` — each failure's `last error` and log forms from the table | R8, R16 support |
| `TestPaired` — `credential.Paired` for: paired; no `state.json`; not confirmed; no entry; unreadable store; damaged entry | R4, R16 support |
| `TestKeepsTryingForever` — many failed tries never stop the loop or change `connect` | R2, R19 |
| `TestOnlyUnknownCredentialStops` — `500`, `404`, other `401` bodies, refused connections, and timeouts all lead to another try | R19 |
| `TestRejectionStops` — `401 unknown_credential` stops trying, writes `connect: false` and `stop_reason`, logs the line | R18 |
| `TestConnectAfterRejection` — `Connect` tries again; a second rejection stops again | R20 |
| `TestIdleWhileConnectFalse` — no network activity while `connect` is false; `Status` still answers | R3 |
| `TestDaemonConnectResults` — `not_paired`, `already_connected`, `connected`, `rejected`, `still_trying` after 30 s (injected clock), and `still_trying` with `can't read state.json` for an unreadable `state.json`, which is left byte-for-byte unchanged; `failed` with the path when writing `state.json` fails, and the loop is not woken (no dial) | R3, R20, R4 |
| `TestUnreadableStateStaysIdle` — with an unparsable `state.json`, `Run` stays idle with no network, writes nothing, and `Status` is `not_connecting` | R3 |
| `TestConnectReadsNewPairing` — after the pairing changes, `Connect` uses the new server and credential | R20 |
| `TestStateKeepsPairingFields` — writing `connect` keeps the other fields; a file without `connect` reads as false | R1 data rule |
| `TestLogLines` — the exact lines for each event, and no line for `ping`/`pong`/`hello` | R8 |
| `TestCredentialNeverInLogOrState` | R25 |
| `TestStopsCleanlyOnContextEnd` — closes with `1000`, `Run` returns `nil` | R12 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- The socket, the lock, and `tervi daemon` (task 04).
- systemd, `tervi connect`, `tervi status`, `tervi logs` (task 05).
- Changes to `tervi pair` (task 06).

## If anything is unclear

Stop and report the question. Do not guess.
