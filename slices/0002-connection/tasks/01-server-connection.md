# Task 01 — Server: the machine connection

Slice: 0002-connection    Risk: core    Depends on: 08

## Goal

Let a paired machine open a WebSocket to the server and keep it open. The
server checks the credential before the WebSocket opens, keeps at most one
connection per machine in memory, runs the timeout check, records "last
seen", and closes every connection cleanly when it stops. This task also adds
the shared message types both sides use.

## Requirements (copied from the spec)

- R12. When either side closes the connection on purpose, the other side shall
  notice at once. *This task: the server side, and the server closing every
  connection when it stops.*
- R13. If the other side stops answering without closing (for example, the
  network is lost), then each side shall notice within the timeout: 30
  seconds by default. *This task: the server side.*
- R14. The check interval (default 10 seconds) and the timeout (default 30
  seconds) shall be set on the server only. The server shall tell each worker
  the values when it connects, so both sides always use the same ones.
- R15. The server shall keep at most one connection per machine. When a new
  connection arrives for a machine that already has one, the server shall
  close the old one, keep the new one, and log a warning naming the machine.
- R18. If the server answers that it does not know this computer's credential,
  then the daemon shall stop trying, set `connect` to false, save the reason in
  `state.json`, and print that the credential was rejected and how to pair
  again. *This task: the server's answer only.*
- R19. The daemon shall treat only that answer as a rejection. A refused
  connection, a timeout, a network error, or a server error shall lead to
  another try, never to `connect` false. *This task: the server sends that
  answer only for an unknown credential.*
- R23. A machine shall show as Online exactly while the server has its
  connection open. After the server restarts, every machine shall show as
  Offline until it connects again. *This task: the in-memory list that says
  which machines are connected.*
- R24. The server shall record when it last heard from each machine: when it
  connects, at least once a minute while connected, and when the connection
  ends (the time of the last message received). "Last seen" may be up to one
  minute early if the server itself stopped suddenly.
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`. *This task: server logs.*
- R34. When the server answers `internal_error`, it shall log what kind of
  operation failed, and never the error's details, a request value, a key,
  a credential, or a hash. *This task: the connection route's own failures,
  through task 08's helper.*

## Context

### What exists (task 08)

`internal/server/httpapi` holds the shared JSON helpers: `BearerProof`,
`WriteJSON`, `WriteError`, and `WriteInternalError(w, logger, kind, err)`,
which logs only the kind (and the PostgreSQL error code). `pairing` already
uses them. Use them in the connection route; never write JSON answers or
read the proof another way. Pass a short fixed kind text for every
`WriteInternalError` call, for example `"look up machine"` or
`"write last seen"`.

### Library

Add `github.com/coder/websocket` (latest v1 release) to `go.mod`. Use it on
both sides; no other WebSocket library.

### Shared messages: `internal/protocol`

The full rules are in `api/asyncapi.yaml`; read it. Build:

- `Hello{Type, CheckIntervalSeconds, TimeoutSeconds}`, `Ping{Type}`,
  `Pong{Type}`, with JSON field names exactly as in `asyncapi.yaml`
  (`type`, `check_interval_seconds`, `timeout_seconds`).
- Type names `"hello"`, `"ping"`, `"pong"` as constants.
- A function that reads the `type` of a received JSON object, so a receiver
  can ignore unknown types.
- `MaxMessageSize = 4096` bytes.
- Close codes as constants, one for each code in `asyncapi.yaml`:
  `CloseNormal = 1000` (the worker stopping on purpose),
  `CloseServerStopping = 1001`, `CloseInvalidMessage = 1008`,
  `CloseTooLarge = 1009` (sent by the library; the constant is for reading
  it), `CloseReplaced = 4000`, and `CloseTimeout = 4001`.
- `DefaultHelloTimeout = 30 * time.Second` (what the worker uses before
  `hello` arrives).

### Database: migration `0002`

`internal/server/db/migrations/0002_last_seen.sql`, goose format like
`0001_pairing.sql`:

```sql
-- +goose Up
ALTER TABLE machines ADD COLUMN last_seen_at timestamptz;
-- +goose Down
ALTER TABLE machines DROP COLUMN last_seen_at;
```

`design/data-model.md` already describes the column. Update any test that
expects database version 1 so that it expects 2.

### Settings

Two new settings in `internal/server/config.go`, read like the others:

| Variable | Default | Rule |
| --- | --- | --- |
| `TERVI_CHECK_INTERVAL_SECONDS` | `10` | whole number, from 1 to 60 |
| `TERVI_CONNECTION_TIMEOUT_SECONDS` | `30` | whole number, greater than the interval |

The interval is at most 60 seconds so that a connected machine's `ping`
arrives, and `last_seen_at` can be written, at least once a minute (R24).
A wrong value stops the server with
`TERVI_CHECK_INTERVAL_SECONDS must be a whole number of seconds, from 1 to 60`
or `TERVI_CONNECTION_TIMEOUT_SECONDS must be a whole number of seconds, greater than TERVI_CHECK_INTERVAL_SECONDS`.
Add both variables, with their defaults and a one-line comment each, to
`.env.example`.

### The route: `internal/server/connection`

`Register(mux, Deps)` adds `GET /api/v1/machines/current/connection`. It
runs behind the existing Host check like every route.

0. A request that carries an `Origin` header gets
   `403 {"error": "forbidden_origin"}` before any other step. Only browsers
   send `Origin`; the worker never does, so no web page can open a machine's
   connection. With no `Origin` header, the library's own origin check
   passes; do not set `InsecureSkipVerify`.
1. Read the proof from `Authorization: Bearer <credential>`. Look it up:
   `SELECT machine_id FROM machines WHERE credential_hash = $1 AND status = 'active'`
   with `secret.Hash(...)`, as `internal/server/pairing/machine.go` does.
2. Missing proof, or no active machine: answer
   `401 {"error": "unknown_credential"}` and do not open a WebSocket. This
   is the only `401` this route sends. A database failure answers
   `500 {"error": "internal_error"}`.
3. Check that the request is a WebSocket opening request before handing it
   to the library: `Connection` contains `upgrade`, `Upgrade` is
   `websocket` (both case-insensitive), `Sec-WebSocket-Version` is `13`,
   and `Sec-WebSocket-Key` is present and is standard base64 of exactly 16
   bytes. Otherwise answer `400 {"error": "invalid_input"}` (documented in
   `api/openapi.yaml`), so the library never answers in plain text.
4. Accept the WebSocket. Set the read limit to `protocol.MaxMessageSize`.
5. Add it to the in-memory list (below), set `last_seen_at = now()`, and send
   `hello` with the configured interval and timeout.
6. Read messages until the connection ends:
   - Every message received, of any type, restarts the timeout count and
     becomes "the last message received".
   - `ping` → send `pong`.
   - An unknown `type` is ignored. A message that is not a JSON object with a
     string `type` closes the connection with `1008`.
   - When a message arrives and `last_seen_at` was last written
     `60 seconds − check interval` or more ago (50 seconds with the default
     interval; every message when the interval is 60), write
     `last_seen_at = now()`. A write that fails does not count as written,
     so the next message tries again. (Decided by the user during task 01:
     the first rule, "a minute or more ago", could leave a gap of nearly 70
     seconds.)

   **When something fails after the WebSocket opens** (added during task 01
   review): the server never sends a close code that `api/asyncapi.yaml`
   does not list.
   - A `last_seen_at` write that fails (in step 5, step 6, or step 8) is
     logged with `httpapi.LogInternalError` and kind `"write last seen"`;
     the connection stays open.
   - A failed socket write (`hello` or `pong`) means the connection is
     already broken: end it with `CloseNow()`, which sends no close code.
   - A `hello` that cannot be encoded is logged with kind `"send hello"`,
     and the connection ends with `CloseNow()`.
7. Timeout: when nothing has been received for the timeout (counted from the
   last message received, or from when the connection opened), close with
   `4001`.
8. When the connection ends for any reason: remove its entry (the
   same-connection rule below) and write `last_seen_at` = the time of the
   last message received, or the opening time if none arrived.

**One clock.** Every `last_seen_at` write uses the database's clock: `now()`
on connect and on a message, and at the end
`now() - make_interval(secs => $1)`, where `$1` is the time since the last
message received (or since opening), measured with Go's monotonic clock.
Task 02 computes "seconds ago" with the database's `now()` too.

### The in-memory list

One entry per `machine_id`, holding that machine's current connection. It
must stay correct when many connections are added, replaced, removed, and
read at the same moment (protect it with a mutex; run its tests with
`-race`).

- Adding when the machine already has an entry: close the old connection with
  `4000`, store the new one, and log
  `slog.Warn("machine connection replaced", "machine_id", id)`.
- Removing: only if the entry is still that same connection. Closing a
  replaced connection must never remove the new one.
- `Online(machineID string) bool` and `OnlineIDs() map[string]bool`, exported
  for task 02.
- `CloseAll()` closes every connection with `1001`, then waits until every
  connection's handler has finished, including its final `last_seen_at`
  write, so nothing writes after the database is closed. It waits at most
  5 seconds.
- `CloseAll()` is final: the list remembers that it was closed. A
  connection added after it (a handshake that finished between `CloseAll`
  and `Shutdown`) is closed at once with `1001` and never stored, and
  `CloseAll` (or a second call to it) waits for that connection's handler
  too, within the same 5 seconds. A server that keeps running never calls
  `CloseAll`.
- **One 5-second limit for the whole stop** (decided by the user during
  the slice review): the server's stop never takes much more than 5
  seconds, however many times `Run` calls `CloseAll`. All the calls share
  one deadline that starts at the first call. A connection whose worker
  does not answer the `1001` close before the deadline is closed at once
  (`CloseNow`); its final `last_seen_at` write still runs if the deadline
  allows. Today a silent worker can make the stop take about 15 seconds.

Every machine is Offline after a restart because the list starts empty.

### Stopping the server

`http.Server.Shutdown` does not close WebSocket connections. The list is a
type in `internal/server/connection`, created with `connection.NewList()`.
Change `server.New` to
`New(cfg Config, pool *pgxpool.Pool, logger *slog.Logger, connections *connection.List) http.Handler`,
so a test can hold the list it passes in (task 07 does). `Run` creates the
list, passes it to `New`, and when the context ends calls
`connections.CloseAll()` first, then `Shutdown`, and only then lets the
database pool close. Update every caller of `server.New`, including
`test/e2e/pairing_test.go` (only that call) and the server's own tests.

`Config` gains `CheckInterval` and `ConnectionTimeout` (`time.Duration`).
A `Config` whose two values are zero (as tests build it) uses the defaults,
10 and 30 seconds.

### Contract

In `api/openapi.yaml`, remove `x-planned: true` from `openConnection` only.
In `test/contract/contract_test.go`, call `openConnection` so that it returns
`101` (with a real credential from a finished pairing, using a WebSocket
handshake), `400` (a known credential without the upgrade headers), `401`,
and `403` twice: through the existing unknown-host loop (`unknown_host`) and
with an `Origin` header (`forbidden_origin`).

### Secrets

Never log the credential, the `Authorization` header, or the hash.

## Boundaries

- May create or change: `internal/protocol/`, `internal/server/connection/`,
  `internal/server/db/migrations/0002_last_seen.sql`,
  `internal/server/config.go`, `internal/server/server.go`, their tests,
  tests that check the database version, `cmd/server/main.go` only if
  `server.New` or `server.Run` callers need it,
  `test/contract/contract_test.go`, `test/e2e/pairing_test.go` (only the
  `server.New` call), `.env.example` (the two settings),
  `api/openapi.yaml` (only the `x-planned` line of `openConnection`),
  `go.mod`, `go.sum`, `internal/server/httpapi/` (only to add
  `LogInternalError(logger, kind, err)`, which `WriteInternalError` calls,
  and its test; added during task 01 review).
- Must not change: anything under `internal/worker/`, anything else in
  `internal/server/httpapi/` (report a missing piece instead),
  `internal/server/pairing/`, `internal/server/web/`, `design/`, `slices/`
  except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUnknownCredentialRefused` — missing, unknown, and `pending`/`failed` credentials get `401 unknown_credential` and no WebSocket | R18, R19 |
| `TestDatabaseFailureIsNotRejection` — a database failure answers `500`, never `401`, and the server log has the kind text and no credential | R19, R34 |
| `TestOriginRefused` — any `Origin` header gets `403 forbidden_origin` in JSON, before the credential is looked up | contract |
| `TestHelloCarriesTiming` — the first message is `hello` with the configured interval and timeout | R14 |
| `TestPingGetsPong` | R13 |
| `TestServerTimeout` — with a 1 s interval and 3 s timeout, a silent client is closed with `4001` and its entry removed; a client that keeps pinging stays | R13 |
| `TestClientCloseNoticedAtOnce` — a client close removes the entry at once | R12 |
| `TestCloseAllOnStop` — cancelling `Run`'s context closes every connection with `1001`, and each connection's final `last_seen_at` is written before `Run` returns | R12, R24 |
| `TestAddAfterCloseAll` — a connection added after `CloseAll` is closed at once with `1001`, is never Online, and its handler is waited for | R12, R24 |
| `TestCloseAllSharesDeadlineAndForcesClose` — three `CloseAll` calls with a worker that never answers the close finish within about 5 s together, and the connection is closed with `CloseNow` (added in fix 1) | R12 |
| `TestNotAnUpgradeRequest` — a known credential without the upgrade headers, or with a missing or malformed `Sec-WebSocket-Key`, gets `400 invalid_input` in JSON | contract |
| `TestNewestWins` — a second connection for the same machine closes the first with `4000`, logs the warning with the machine id, and the machine stays Online | R15 |
| `TestReplacedCloseKeepsNewEntry` — the old connection's end does not remove the new entry | R15, R23 |
| `TestListSafeUnderLoad` — many goroutines add, replace, remove, and read at once, under `go test -race` | R15, R23 |
| `TestOnlineListStartsEmpty` | R23 |
| `TestLastSeen` — set on connect; rewritten on a message only once `60 s − check interval` has passed since the last write, and not before (use an injected clock); set to the last message time when the connection ends, using the database's clock | R24 |
| `TestOversizedMessageCloses` | contract rule |
| `TestUnknownTypeIgnored` | contract rule |
| `TestCredentialNeverLogged` — the log output of every test above contains no credential | R25 |
| `TestConnectionTimingSettings` — defaults, valid values, an interval of 61, and both error messages | R14, R24 |
| `TestMigrationAddsLastSeen` — version 2, the column exists and is null for existing machines | R24 |
| Contract test — `openConnection` returns `101`, `400`, `401`, and `403` (`unknown_host` and `forbidden_origin`); no `x-planned` on it | contract |

`go vet ./...`, `go test ./...`, and `go test -race ./internal/server/connection/`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- The shared JSON helpers and logging each `500` (task 08).
- The machines list and the Machines page (task 02).
- Everything on the worker (tasks 03–06).

## If anything is unclear

Stop and report the question. Do not guess.
