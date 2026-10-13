# 0002 Connection — plan

Status: draft
Spec: [spec.md](spec.md)

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| JSON answers and proofs | `internal/server/httpapi` | The server's JSON API conventions, shared by its route packages: reading the `Authorization: Bearer` proof, writing JSON answers and `{"error": …}` answers, and logging the kind of each `500` (R34). Moved here from `internal/server/pairing` (task 08) |
| Connection on the server | `internal/server/connection` | Accepts each worker's connection, checks its credential, keeps one connection per machine (newest wins), runs the checks that notice a lost worker, knows which machines are Online, and records "last seen"; `Register(mux, ...)` for its route |
| Machines list | `internal/server/machines` | The list the Machines page shows: every paired machine with its name, OS, Online or Offline, and "last seen"; `Register(mux, ...)` for its route |
| Machines page | `internal/server/web` | `/machines`, plain HTML and JavaScript next to the approval page, refreshing itself every 5 seconds |
| Database | `internal/server/db` | A migration that adds "last seen" to machines |
| Connection messages | `internal/protocol` | Shared by both sides: the messages sent over the connection, and the check timing the server sends the worker |
| Daemon | `internal/worker/daemon` | The long-running engine: obeys `connect`, connects, reconnects with growing waits, stops on a rejected credential, writes the log lines |
| Talking to the daemon | `internal/worker/control` | The runtime folder, the lock that allows one daemon per user account, the socket `tervi status` and `tervi connect` use to reach the running daemon, and its answers |
| Background service | `internal/worker/service` | Sets up, starts, and enables the systemd user service; checks that tervi is installed (R28); reads the log for `tervi logs` |
| Worker command line | `internal/worker/cli` | Adds `tervi connect`, `tervi status`, `tervi logs`, and `tervi daemon`; after pairing, starts the background service and waits for the connection |
| Worker local record | `internal/worker/state` | Adds `connect` and the rejection reason to `state.json` |
| Worker credential | `internal/worker/credential` | Adds one "is this computer paired?" check (a confirmed `state.json` and a secret store entry), used by the daemon and the commands |
| End-to-end test | `test/e2e` | Adds the connection: pairing connects, the server stops and comes back, a rejected credential |
| API contract | `api` | `openapi.yaml` gets the connection route and the machines list; a new `asyncapi.yaml` holds the messages sent over the connection |

The new server packages register their own routes, as in slice 1, so tasks
that add routes do not edit the same file.

## Data

This slice adds the `last_seen_at` column to `machines` (migration `0002`),
the `connect` and `stop_reason` fields to `state.json`, and the systemd
service file. All of it is described in
[`design/data-model.md`](../../design/data-model.md). Which machines are
Online is kept only in the server's memory (R23).

## Interfaces

### Server routes

Added to [`api/openapi.yaml`](../../api/openapi.yaml):

| Operation | Called by | Proof | Purpose | Requirements |
| --- | --- | --- | --- | --- |
| `GET /api/v1/machines/current/connection` | The daemon | Credential | Opens the WebSocket; a request with an `Origin` header gets `403 forbidden_origin`, and an unknown credential gets `401 unknown_credential`, before it opens | R2, R12–R15, R18, R19, R23, R24 |
| `GET /api/v1/machines` | The Machines page | None (R27) | Every active machine, with Online and "last seen" | R21, R23, R24 |
| `GET /machines` | The browser | None (R27) | The Machines page (HTML) | R21, R22 |

### Connection messages

Described in the new [`api/asyncapi.yaml`](../../api/asyncapi.yaml):

| Message | From | Purpose |
| --- | --- | --- |
| `hello` | Server, first message | The check interval and the timeout (R14) |
| `ping` | Worker, every interval | "I am here" |
| `pong` | Server, answering each `ping` | "So am I" |

The close codes the server uses (server stopping, replaced by a newer
connection, timeout) are listed there too, so the daemon can log the reason
(R8).

### Talking to the daemon

HTTP over the Unix socket `$XDG_RUNTIME_DIR/tervi/daemon.sock`. Only the
worker's own commands call it, so it is described here, not in `api/`.

| Operation | Called by | Answer |
| --- | --- | --- |
| `GET /status` | `tervi status` | The state: `connected` (server, connected since), `reconnecting` (server, try number, seconds until the next try, last error), `not_connecting` (the stop reason), or `not_paired` |
| `POST /connect` | `tervi connect`, and `tervi pair` after pairing | Sets `connect` to true, reads the pairing again, and answers once the result is known or 30 seconds have passed: `connected`, `already_connected`, `still_trying` (with the last error), `rejected`, or `not_paired` |

A program from another user account is refused before any operation runs
(R26).

### Worker commands

| Command | Does | Exit code |
| --- | --- | --- |
| `tervi pair --server <url>` | As in slice 1; first checks that tervi is installed (R28); after pairing, sets up and starts the service and calls `POST /connect` (R5, R10, R11, R29, R31) | `0` paired, `1` failure, `2` wrong usage, `130` Ctrl+C |
| `tervi connect` | Sets up and starts the service if needed, then calls `POST /connect` (R4, R20, R31) | `0` connected or already connected, `1` otherwise |
| `tervi status` | Reads `state.json`, then calls `GET /status` (R16, R17) | `0` connected, `1` otherwise |
| `tervi logs [-f]` | Reads the service's log from the journal (R32) | `0`, or `1` if the journal cannot be read |
| `tervi daemon` | Runs the daemon in this terminal (R30); the service runs the same command | `0` when stopped with Ctrl+C or by systemd, or when another daemon is already running; `1` on an unexpected failure |

`tervi daemon` exits with `0` when another daemon is already running, so
that systemd (which restarts only after a failure) does not keep starting
it again while the user runs one by hand.

## Mechanisms

### Checks and timeout

Applies to every connection. Covers R12–R14. The rules are in
[`api/asyncapi.yaml`](../../api/asyncapi.yaml): the server sends `hello`
with the interval and the timeout (defaults 10 and 30 seconds, set in the
server's settings); the worker sends `ping` every interval, counted from its
previous `ping`; the server answers `pong`; each side closes the connection
after the timeout with no message of any kind from the other side. A close
by either side on purpose is noticed at once, because the WebSocket close
reaches the other side.

### Reconnecting

Applies to the daemon whenever `connect` is true and it has no connection.
Covers R2, R9, R19. The base wait before try *n* is 1, 2, 4, 8, 16
seconds for tries 1–5, and 30 seconds for every later try; each actual wait
is made longer or shorter at random by up to 20%. After a lost connection,
the daemon waits before try 1. The first try after the daemon starts, or
after `POST /connect`, is try 1 with no wait. The count starts again from 1
once a connection succeeds, which means `hello` arrived. Every failure
leads to another try except a `401` (next mechanism); a secret store that
cannot be read is a failed try too, read again on every try, so a keyring
that unlocks a little after login still connects. Each try's request waits
at most 10 seconds, as in slice 1.

### Rejection

Covers R18–R20. A `401 unknown_credential` answer to the opening request
makes the daemon stop trying, set `connect` to false and `stop_reason` to
`credential_rejected`, and log it. `POST /connect` sets `connect` back to
true, reads the pairing again, and tries as usual (R2); a `401` on its
first try stops it again the same way (R20).

### One connection per machine

Covers R15, R23. The server keeps the open connections in memory, one per
machine. When a machine connects while it already has a connection, the
server closes the old one with code `4000`, keeps the new one, and logs a
warning naming the machine. A machine is Online exactly while it has an
entry. When a connection ends, the server removes the machine's entry only
if the entry is still that same connection, so closing a replaced
connection never marks a connected machine Offline. `GET /api/v1/machines`
reads each machine's name, OS, and `last_seen_at` from the database, and
asks the in-memory list whether it is Online. The in-memory list stays
correct when many connections are added, replaced, removed, and read at the
same moment.

### Last seen

Covers R24. The server writes `last_seen_at` when a machine connects, when
a message arrives and the last write was a minute or more ago, and when the
connection ends (the time of the last message received). The check
interval is at most 60 seconds, so a connected machine sends a message, and
gets a write, at least once a minute. Every write uses the database's
clock: `now()`, or at the end `now()` minus the time since the last message
as measured by the server. The machines list also computes "seconds ago"
with the database's clock, so the two never disagree.

### Server stopping

Covers R12, R24. On Ctrl+C, the server closes every connection with code
`1001` and waits until each connection has written its final
`last_seen_at`, before it stops serving and closes the database. Each worker
notices at once and starts reconnecting. Closing every connection is final:
a connection that finishes opening after it is closed at once with `1001`
and waited for too, so nothing writes after the database is closed.

### Starting the background service

Applies to `tervi pair` after pairing and to `tervi connect`. Covers R4,
R5, R10, R11, R28, R29, R31. Steps 2–3 take at most 10 seconds; step 4
waits at most 30 seconds (R11):

1. Check that tervi is installed: the running program's path must not be
   inside the system's temporary folder or a `go-build` folder, where
   `go run` builds programs (R28). `tervi pair` does this before contacting the server.
2. If the service file is missing or names another program path, write it
   (as in [`design/data-model.md`](../../design/data-model.md)), run
   `systemctl --user daemon-reload`, `systemctl --user enable tervi`, and
   `systemctl --user restart tervi`, so a daemon still running an old
   program file is replaced by the new one (R5). Otherwise run
   `systemctl --user enable --now tervi`.
3. Wait for the daemon's socket to answer.
4. Call `POST /connect` and print the result.

A failure in steps 2–3 prints the reason and `Try again with: tervi connect`
(R29); the pairing stays saved. After the last line, the command exits
(R31).

### One daemon per account

Covers R7, R30. A starting daemon first takes the lock on
`$XDG_RUNTIME_DIR/tervi/daemon.lock`, then creates the socket. A daemon
that cannot get the lock prints that tervi is already running and exits
with `0`.

## Technical decisions

- Only the daemon writes `connect` and `stop_reason` in `state.json`;
  `tervi pair` and `tervi connect` ask the running daemon, starting it first
  if needed — with one writer, no change can be lost, and the daemon always
  runs anyway (R3); `tailscale` and `tailscaled` work the same way.
  Not chosen: every program writes the whole file — two writes at the same
  moment lose one change; every program writes with a file lock — works,
  but every writer must take the lock correctly, and one that forgets
  loses changes again.
- `state.json` gains `connect` and `stop_reason`; a missing `connect` means
  `false` — a computer paired in slice 0001 has no background service yet,
  so it needs `tervi connect` once anyway.
  Not chosen: a missing `connect` means `true` — the daemon would not be
  running to act on it, so it would change nothing.
- The service file is `~/.config/systemd/user/tervi.service`, runs the full
  path of the installed `tervi` with `daemon`, restarts only after a crash
  (5 seconds later), and starts at login — `~/.config` is the folder for
  services a user sets up; systemd does not use the user's `PATH`; R6 asks
  for a restart only when it stops unexpectedly.
  Not chosen: `~/.local/share/systemd/user/` — meant for software installed
  by a package; `ExecStart=tervi daemon` — systemd might not find it;
  `Restart=always` — it would also restart after a clean stop.
- The worker and the server talk over one WebSocket that the worker opens
  and keeps open — both sides can send at any time, a close is noticed at
  once (R12), and later slices run agents and terminals over it.
  Not chosen: long polling — only the worker can start a message, and a lost
  connection is harder to notice; a gRPC stream — code generation and a
  heavy toolset, and browsers cannot use it directly.
- The WebSocket library is `github.com/coder/websocket` — small, every call
  takes a `context` so Ctrl+C and timeouts work the Go way, and it is
  maintained by a company whose product works much like tervi.
  Not chosen: `github.com/gorilla/websocket` — its API predates `context`,
  and it was archived in 2022 before new maintainers revived it.
- The worker proves who it is in the request that opens the WebSocket, with
  `Authorization: Bearer <credential>`; an unknown credential gets `401`
  with `{"error": "unknown_credential"}` before any WebSocket opens, and
  that answer is the only rejection (R18, R19) — the server never opens a
  connection for a stranger, and the rejection cannot be confused with
  anything else.
  Not chosen: the credential in the first message — the server would open
  connections for anyone, and a rejection would arrive as a close code that
  is easy to confuse with other closes; the credential in the address —
  addresses end up in logs.
- The worker sends `ping` and the server answers `pong`, as tervi's own JSON
  messages, at the interval the server sends in its first message; each
  side gives up after the timeout counted from the last message of any kind
  it received from the other side (or from the moment the connection opened,
  before the first one): the worker then reconnects, and the server closes
  the connection — the
  worker is the side that must act when the connection dies, the server only
  has to notice silence, and each `ping` also tells the server when it last
  heard from the machine (R24); MQTT works the same way.
  Not chosen: the server sends `ping` — the server would need a timer per
  worker that sends, and the worker would act on the absence of a message
  rather than on a missing answer; WebSocket's built-in ping frames — the
  library answers them without telling tervi's code, and they do not appear
  in the contract.
- `tervi status` and `tervi connect` reach the daemon over a Unix socket,
  `$XDG_RUNTIME_DIR/tervi/daemon.sock`, with HTTP on it (`GET /status`,
  `POST /connect`); the folder is readable only by the user, and the daemon
  also asks Linux for the connecting program's user and refuses any other
  (R26); a command that cannot connect to the socket reports that the daemon
  is not running (R16) — private by design with no password or token, Go's
  HTTP code and tests work unchanged, and `tailscale` reaches `tailscaled`
  the same way.
  Not chosen: a port on `127.0.0.1` — any account on the computer, and
  websites in the user's browser, could reach it, so it would need a token;
  request files and signals — slow, files left behind after a crash, and two
  commands at once can mix up their files.
- Only one daemon runs per user account because each daemon first takes an
  exclusive lock (`flock`) on `$XDG_RUNTIME_DIR/tervi/daemon.lock`, and one
  that cannot get it prints that tervi is already running and exits (R7,
  R30) — taking the lock is one step, so two daemons starting together
  cannot both win, and Linux releases it when the daemon dies, so a crash
  never leaves a stuck lock.
  Not chosen: checking whether the socket answers — a crash leaves the old
  socket file behind, which needs extra code to tell apart from a running
  daemon, and checking then creating are two steps that two daemons
  starting together can both pass.
- Operations a plan adds to `api/openapi.yaml` are marked `x-planned: true`,
  and the contract test does not require a marked operation to be called;
  the task that builds it removes the mark, and the slice review checks that
  none is left — the contract stays in the plan pull request, where the user
  approves it, and `go test ./...` stays green on `main` and on every task
  branch before the code exists.
  Not chosen: each task adds its own operation to the contract — the user
  would no longer approve the contract before it is built; accepting failing
  tests until the slice is merged — real failures would hide among the
  expected ones.
- Each reconnect wait (R9) is made longer or shorter at random by up to 20%
  — when the server stops or restarts, every worker notices in the same
  instant, and with exact waits every worker would try again in the same
  instant; the most common restart is a short one, so every wait needs it,
  not only the later ones.
  Not chosen: exact waits — every worker arrives at once after a restart;
  randomness only from the sixth try, then exactly 30 seconds — the
  offsets would last, but a restart shorter than the first five tries would
  still bring every worker at once.
- tervi gives instructions to systemd by running its commands
  (`systemctl --user daemon-reload`, `enable`, `restart`, `enable --now`,
  `journalctl --user -u tervi`) — no new library, the steps are the ones a
  user would type, systemd's own error messages can be shown as they are,
  and `tervi logs` shows exactly what `journalctl` would.
  Not chosen: `github.com/coreos/go-systemd` over D-Bus — a new library and
  more code, and reading the journal with it needs C code (cgo), which
  complicates building.
- `tervi daemon` exits with `0` when another daemon already runs — systemd
  (`Restart=on-failure`) leaves a clean exit alone.
  Not chosen: exit `1` plus `RestartPreventExitStatus=1` in the service file
  — an extra special line; exit `1` alone — systemd would restart it every
  5 seconds forever while the user runs one by hand.
- A program counts as temporary (R28) when its resolved path is inside
  `os.TempDir()` or inside a folder named `go-build` — catches `go run` even
  when Go's temporary folder is moved (`GOTMPDIR`).
  Not chosen: only inside `os.TempDir()` — misses a moved Go temporary
  folder; "is it the `tervi` on `PATH`" — wrongly refuses a tervi installed
  outside `PATH`.
- `last_seen_at` is rewritten when a message arrives and the last write was
  a minute or more ago, with the check interval capped at 60 seconds so the
  minute always holds — no extra timer.
  Not chosen: a timer per connection writing every minute — works with no
  messages, but adds a timer for every machine.
- (pending the user's confirmation) The connection route refuses any
  request that carries an `Origin` header with `403 forbidden_origin`,
  before anything else — only browsers send `Origin`, and the worker never
  does, so no web page can open a machine's connection.
  Not chosen: the WebSocket library's own `Origin` check — it answers in
  plain text, breaking the rule that every `/api/` answer is JSON; skipping
  the `Origin` check — a page in the user's browser could open the
  connection if it ever had a credential.

## Requirement coverage

| Requirement | Tasks |
| --- | --- |
| R1 | 03, 06, 07 |
| R2 | 03, 07 |
| R3 | 03, 04 |
| R4 | 05 |
| R5 | 05, 06 |
| R6 | 05 |
| R7 | 04 |
| R8 | 03, 04 |
| R9 | 03 |
| R10 | 06, 07 |
| R11 | 06 |
| R12 | 01, 03, 07 |
| R13 | 01, 03 |
| R14 | 01, 03 |
| R15 | 01, 07 |
| R16 | 05, 07 |
| R17 | 05 |
| R18 | 01, 03, 07 |
| R19 | 01, 03 |
| R20 | 03 |
| R21 | 02, 07 |
| R22 | 02 |
| R23 | 01, 02, 07 |
| R24 | 01, 02, 07 |
| R25 | 01, 02, 03, 05, 06, 07, 08 |
| R26 | 04 |
| R27 | 02 |
| R28 | 05, 06 |
| R29 | 05, 06 |
| R30 | 04 |
| R31 | 05, 06 |
| R32 | 05 |
| R33 | 06, 07 |
| R34 | 08, 01, 02 |

R1 is met through the daemon: pairing asks it to connect, and it writes
`connect: true` (decision 1). If the background service cannot start, the
user finishes with `tervi connect` (R29).

## Tasks

| # | Task | Depends on | Risk | Status |
| --- | --- | --- | --- | --- |
| 08 | [Server: shared JSON helpers, and logging each `500`](tasks/08-server-http-helpers.md) | — | normal | todo |
| 01 | [Server: the machine connection](tasks/01-server-connection.md) | 08 | core | todo |
| 02 | [Server: the machines list and the Machines page](tasks/02-machines-page.md) | 01, 08 | normal | todo |
| 03 | [Worker: the daemon's connection](tasks/03-daemon-connection.md) | 01 | core | todo |
| 04 | [Worker: the daemon's socket, its lock, and `tervi daemon`](tasks/04-daemon-socket.md) | 03 | core | todo |
| 05 | [Worker: the background service, `tervi connect`, `tervi status`, `tervi logs`](tasks/05-service-and-commands.md) | 04 | core | todo |
| 06 | [Worker: pairing hands over to the daemon](tasks/06-pairing-hands-over.md) | 05 | core | todo |
| 07 | [End-to-end test of the connection](tasks/07-end-to-end.md) | 01–06, 08 | normal | todo |

The table is in build order. Task 08 is built first and numbered 08 only
because it was split out of task 01 during plan review, after the other
task files were numbered.

Tasks 02 and 03 can be built at the same time. Both change their own row
in this table; the rows are next to each other, so the second merge into
the slice branch has a one-line conflict, which the architect resolves.

Task sizes are uneven: task 03 (the daemon's engine) and task 01 (the
protocol, the connection route and list, settings, the migration, and the
contract test) are the largest, and tasks 06, 07, and 08 are small. This is
accepted: splitting task 03 would separate the connection loop from the
methods that drive it; task 01's parts are one route and what it needs, and
the shared helpers it used to carry are now task 08; task 07 is tests only.
