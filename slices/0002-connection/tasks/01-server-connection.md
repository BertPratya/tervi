# Task 01 — Server: the machine connection

Slice: 0002-connection    Risk: core    Depends on: —

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

## Context

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
- Close codes as constants: `CloseServerStopping = 1001`,
  `CloseReplaced = 4000`, `CloseTimeout = 4001`, and the worker's normal
  close `1000`.
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
| `TERVI_CHECK_INTERVAL_SECONDS` | `10` | whole number, at least 1 |
| `TERVI_CONNECTION_TIMEOUT_SECONDS` | `30` | whole number, greater than the interval |

A wrong value stops the server with
`TERVI_CHECK_INTERVAL_SECONDS must be a whole number of seconds, at least 1`
or `TERVI_CONNECTION_TIMEOUT_SECONDS must be a whole number of seconds, greater than TERVI_CHECK_INTERVAL_SECONDS`.

### The route: `internal/server/connection`

`Register(mux, Deps)` adds `GET /api/v1/machines/current/connection`. It
runs behind the existing Host check like every route.

1. Read the proof from `Authorization: Bearer <credential>`. Look it up:
   `SELECT machine_id FROM machines WHERE credential_hash = $1 AND status = 'active'`
   with `secret.Hash(...)`, as `internal/server/pairing/machine.go` does.
2. Missing proof, or no active machine: answer
   `401 {"error": "unknown_credential"}` and do not open a WebSocket. This
   is the only `401` this route sends. A database failure answers
   `500 {"error": "internal_error"}`.
3. Accept the WebSocket. Set the read limit to `protocol.MaxMessageSize`.
4. Add it to the in-memory list (below), set `last_seen_at = now()`, and send
   `hello` with the configured interval and timeout.
5. Read messages until the connection ends:
   - Every message received, of any type, restarts the timeout count and
     becomes "the last message received".
   - `ping` → send `pong`.
   - An unknown `type` is ignored. A message that is not a JSON object with a
     string `type` closes the connection with `1008`.
   - When a message arrives and `last_seen_at` was last written a minute or
     more ago, write `last_seen_at = now()`.
6. Timeout: when nothing has been received for the timeout (counted from the
   last message received, or from when the connection opened), close with
   `4001`.
7. When the connection ends for any reason: remove its entry (the
   same-connection rule below) and write `last_seen_at` = the time of the
   last message received, or the opening time if none arrived.

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
- `CloseAll()` closes every connection with `1001`.

Every machine is Offline after a restart because the list starts empty.

### Stopping the server

`http.Server.Shutdown` does not close WebSocket connections. In
`internal/server/server.go`, create the connection list in `Run`, pass it to
`New` (change `New`'s parameters as needed and update its callers and tests),
and call `CloseAll()` before `Shutdown` when the context ends.

### Contract

In `api/openapi.yaml`, remove `x-planned: true` from `openConnection` only.
In `test/contract/contract_test.go`, call `openConnection` so that it returns
`101` (with a real credential from a finished pairing, using a WebSocket
handshake), `401`, and `403` (through the existing unknown-host loop).

### Secrets

Never log the credential, the `Authorization` header, or the hash.

## Boundaries

- May create or change: `internal/protocol/`, `internal/server/connection/`,
  `internal/server/db/migrations/0002_last_seen.sql`,
  `internal/server/config.go`, `internal/server/server.go`, their tests,
  tests that check the database version, `cmd/server/main.go` only if
  `server.New` or `server.Run` callers need it,
  `test/contract/contract_test.go`, `api/openapi.yaml` (only the
  `x-planned` line of `openConnection`), `go.mod`, `go.sum`.
- Must not change: anything under `internal/worker/`, `internal/server/pairing/`,
  `internal/server/web/`, `design/`, `slices/` except this task's row in
  `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUnknownCredentialRefused` — missing, unknown, and `pending`/`failed` credentials get `401 unknown_credential` and no WebSocket | R18, R19 |
| `TestDatabaseFailureIsNotRejection` — a database failure answers `500`, never `401` | R19 |
| `TestHelloCarriesTiming` — the first message is `hello` with the configured interval and timeout | R14 |
| `TestPingGetsPong` | R13 |
| `TestServerTimeout` — with a 1 s interval and 3 s timeout, a silent client is closed with `4001` and its entry removed; a client that keeps pinging stays | R13 |
| `TestClientCloseNoticedAtOnce` — a client close removes the entry at once | R12 |
| `TestCloseAllOnStop` — cancelling `Run`'s context closes every connection with `1001` | R12 |
| `TestNewestWins` — a second connection for the same machine closes the first with `4000`, logs the warning with the machine id, and the machine stays Online | R15 |
| `TestReplacedCloseKeepsNewEntry` — the old connection's end does not remove the new entry | R15, R23 |
| `TestListSafeUnderLoad` — many goroutines add, replace, remove, and read at once, under `go test -race` | R15, R23 |
| `TestOnlineListStartsEmpty` | R23 |
| `TestLastSeen` — set on connect; rewritten on a message only after a minute (use an injected clock); set to the last message time when the connection ends | R24 |
| `TestOversizedMessageCloses` | contract rule |
| `TestUnknownTypeIgnored` | contract rule |
| `TestCredentialNeverLogged` — the log output of every test above contains no credential | R25 |
| `TestConnectionTimingSettings` — defaults, valid values, and both error messages | R14 |
| `TestMigrationAddsLastSeen` — version 2, the column exists and is null for existing machines | R24 |
| Contract test — `openConnection` returns `101`, `401`, `403`; no `x-planned` on it | contract |

`go vet ./...`, `go test ./...`, and `go test -race ./internal/server/connection/`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- The machines list and the Machines page (task 02).
- Everything on the worker (tasks 03–06).

## If anything is unclear

Stop and report the question. Do not guess.
