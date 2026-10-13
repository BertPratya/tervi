# 0002 Connection — plan

Status: draft
Spec: [spec.md](spec.md)

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| Connection on the server | `internal/server/connection` | Accepts each worker's connection, checks its credential, keeps one connection per machine (newest wins), runs the checks that notice a lost worker, knows which machines are Online, and records "last seen"; `Register(mux, ...)` for its route |
| Machines list | `internal/server/machines` | The list the Machines page shows: every paired machine with its name, OS, Online or Offline, and "last seen"; `Register(mux, ...)` for its route |
| Machines page | `internal/server/web` | `/machines`, plain HTML and JavaScript next to the approval page, refreshing itself every 5 seconds |
| Database | `internal/server/db` | A migration that adds "last seen" to machines |
| Connection messages | `internal/protocol` | Shared by both sides: the messages sent over the connection, and the check timing the server sends the worker |
| Daemon | `internal/worker/daemon` | The long-running program: obeys `connect`, connects, reconnects with growing waits, stops on a rejected credential, allows one daemon per user account, writes the log lines |
| Talking to the daemon | `internal/worker/control` | How `tervi status` and `tervi connect` reach the running daemon, and its answers |
| Background service | `internal/worker/service` | Sets up, starts, and enables the systemd user service; checks that tervi is installed (R28); reads the log for `tervi logs` |
| Worker command line | `internal/worker/cli` | Adds `tervi connect`, `tervi status`, `tervi logs`, and `tervi daemon`; after pairing, starts the background service and waits for the connection |
| Worker local record | `internal/worker/state` | Adds `connect` and the rejection reason to `state.json` |
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
| `GET /api/v1/machines/current/connection` | The daemon | Credential | Opens the WebSocket; an unknown credential gets `401 unknown_credential` before it opens | R2, R12–R15, R18, R19, R23, R24 |
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
| `GET /status` | `tervi status` | The state: `connected` (server, machine name, connected since), `reconnecting` (try number, seconds until the next try, last error), or `not_connecting` (the stop reason) |
| `POST /connect` | `tervi connect`, and `tervi pair` after pairing | Sets `connect` to true, reads the pairing again, and answers once the result is known or 30 seconds have passed: `connected`, `already_connected`, `still_trying` (with the last error), or `rejected` |

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
