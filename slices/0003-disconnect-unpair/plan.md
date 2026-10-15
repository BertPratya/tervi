# 0003 Disconnect and unpair — plan

Status: draft
Spec: [spec.md](spec.md)

This slice has 13 design decisions, more than the 10 that `AGENTS.md`
allows; the user chose to keep it whole (2026-10-14), because the two
decisions for recognizing a computer are too few for a slice of their own.

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| Database | `internal/server/db/migrations/0003_credentials.sql` (new) | Creates `credentials`, moves each machine's credential into it, adds `identity_hash` to `machines` and to `pairing_requests`, and `matched_machine_id` to `pairing_requests` |
| Credential check | `internal/server/credential/credential.go` (new) | One lookup by hash, shared by every route that takes a credential: finds the credential's row and says `active`, `revoked`, or unknown; the status names |
| Ending a credential | `internal/server/credential/revoke.go` (new) | Sets an `active` credential to `revoked` with its end time, inside the caller's transaction; used by unpair, Revoke, and pairing again |
| Unpair route | `internal/server/machines/unpair.go` (new) | `POST /api/v1/machines/current/unpair` (R11, R12) |
| Revoke route | `internal/server/machines/revoke.go` (new) | `POST /api/v1/machines/{machine_id}/revoke` (R18) |
| Machines list | `internal/server/machines` | Lists machines with an `active` credential (R19) |
| Closing a revoked machine | `internal/server/connection` | Closes a machine's connection with `4002` on request; the connection route uses the credential check and answers `credential_revoked` (R18, R20) |
| Recognizing a computer | `internal/server/pairing/match.go` (new) | Finds the earlier machine for an identity, when the pairing starts (R24, R25) |
| Pairing | `internal/server/pairing` | Start stores the identity and the match; the approval answer shows the match; the correct code attaches the new credential to the matched machine; the acknowledgment ends the earlier credential and updates the name (R23–R28); every machine query uses `credentials` |
| Pages | `internal/server/web` | The Machines page gets Revoke and its question (R17, R18); the approval page shows the match with Accept and Reject (R24) |
| Close codes | `internal/protocol` | Adds `4002` |
| Computer identity | `internal/worker/pair/identity.go` (new) | Reads `/etc/machine-id` and computes the identity with tervi's app ID (R23) |
| Pairing on the worker | `internal/worker/pair` | Sends the identity; without a machine ID, asks first (R23); after `credential_revoked`, starts a new pairing (R22) |
| `tervi disconnect` | `internal/worker/cli/disconnect.go` (new) | Asks the daemon, or writes `state.json` under the lock when no daemon runs (R1–R6) |
| `tervi unpair` | `internal/worker/cli/unpair.go` (new) | The question, `--yes`, `--local-only`, and the messages (R10–R16) |
| Unpairing | `internal/worker/unpair/unpair.go` (new) | Tells the server, then removes the pairing on the computer in order (R11–R16) |
| Talking to the daemon | `internal/worker/control` | Adds `POST /disconnect`, and taking the lock when no daemon runs (R1, R3) |
| Taking the lock without a daemon | `internal/worker/control/lock.go` (new) | Takes `daemon.lock`, runs one change, and lets go; the lock code moves here from `daemon.go` |
| Daemon | `internal/worker/daemon` | The run loop calls the two files below (R1, R20) |
| A revoked credential | `internal/worker/daemon/revoked.go` (new) | Stops on `4002` or `credential_revoked`, records it, logs it (R20) |
| Disconnecting | `internal/worker/daemon/disconnect.go` (new) | `Disconnect`: closes with `1000`, cancels an attempt in progress, logs only `○ Disconnected.` (R1) |
| Removing the service | `internal/worker/service/remove.go` (new) | Stops and disables the service, removes its file, and says whether linger is on (R11) |
| Worker command line | `internal/worker/cli` | `tervi status` shows `Disconnected` and the unpaired message, also without a daemon, and `tervi connect` the unpaired message (R7, R21); `--yes` for `tervi pair`; the usage line |
| Machine ID question | `internal/worker/cli/machineid.go` (new) | The question `tervi pair` asks when the computer has no machine ID (R23) |
| Worker local record | `internal/worker/state` | Adds the stop reason `credential_revoked` |
| End-to-end test | `test/e2e` | Adds disconnect, unpair, Revoke, and pairing the same computer again |
| API contract | `api` | `openapi.yaml` gets the unpair and Revoke routes, the identity, the match, and `credential_revoked`; `asyncapi.yaml` gets `4002` |

`internal/server/credential` is a new package because the connection route,
the machines routes, and pairing all check credentials, and the connection
package cannot import the machines package (machines already imports
connection).

## Data

This slice moves each machine's credential into a new table,
`credentials`, so that a machine keeps one row while each pairing adds a
credential row; it adds `identity_hash` to `machines`, and `identity_hash`
and `matched_machine_id` to `pairing_requests` (migration `0003`); and it
adds the stop reason `credential_revoked` to `state.json`. All of it is
described in [`design/data-model.md`](../../design/data-model.md). Whether a
machine is paired is not stored: it is paired while it has an `active`
credential.

## Interfaces

### Server routes

Added to or changed in [`api/openapi.yaml`](../../api/openapi.yaml):

| Operation | Called by | Proof | Purpose | Requirements |
| --- | --- | --- | --- | --- |
| `POST /api/v1/machines/current/unpair` (new) | `tervi unpair` | Credential | Ends the credential and closes the machine's connection; `204`, also when already ended; `401 unknown_credential` when unknown | R11–R13 |
| `POST /api/v1/machines/{machine_id}/revoke` (new) | The Machines page | None (R30) | Ends the machine's `active` credential and closes its connection; `204`, also when already ended; `404 unknown_machine` for a machine that does not exist | R18 |
| `GET /api/v1/machines/current/connection` | The daemon | Credential | As in slice 2; a `revoked` credential gets `401 credential_revoked` | R20 |
| `POST /api/v1/machines/current/acknowledgment` and `…/save-failure` | `tervi pair` | Credential | As in slice 1; the acknowledgment of a pairing that matched an earlier machine also ends that machine's earlier credential; a `revoked` credential gets `401 credential_revoked` | R26 |
| `POST /api/v1/pairings` | `tervi pair` | None | Takes an optional `identity_hash` | R23 |
| `GET /api/v1/approvals/current` | The approval page | Approval key | Adds `earlier_machine` (its name, and when it was unpaired or that it is still paired) when the pairing matched one | R24, R28 |
| `GET /api/v1/machines` | The Machines page | None | Only machines with an `active` credential | R19 |

### Connection messages

[`api/asyncapi.yaml`](../../api/asyncapi.yaml) gets one close code the
server sends: `4002`, "this machine's credential was revoked", when the
machine is unpaired, revoked, or paired again while connected (R18, R26).

### Talking to the daemon

As in slice 2, HTTP over `$XDG_RUNTIME_DIR/tervi/daemon.sock`:

| Operation | Called by | Answer |
| --- | --- | --- |
| `POST /disconnect` (new) | `tervi disconnect` | Sets `connect` to false and closes the connection with `1000`: `disconnected`, `already_disconnected`, `rejected` or `revoked` (nothing changes, R5, R21), `not_paired`, or `failed` (`state.json` could not be written, with its path) |
| `GET /status` | `tervi status` | As in slice 2; `not_connecting` carries the stop reason `credential_revoked` too |
| `POST /connect` | `tervi connect`, `tervi pair` | As in slice 2, plus `revoked` |

### Worker commands

| Command | Does | Exit code |
| --- | --- | --- |
| `tervi disconnect` | Calls `POST /disconnect`; when no daemon runs, writes `connect: false` itself under the lock (R1–R6) | `0`, `1` when not paired or on a failure, `2` wrong usage |
| `tervi unpair [--yes] [--local-only]` | Asks, unless `--yes`; tells the server (not with `--local-only`), then removes the pairing on the computer (R10–R16) | `0` unpaired, `1` otherwise, `2` wrong usage |
| `tervi pair --server <url> [--yes]` | As in slice 2; sends the identity; `--yes` answers the machine ID question (R23); after `credential_revoked`, starts a new pairing (R22) | As in slice 2 |
| `tervi status` | As in slice 2; reads `state.json` when no daemon runs, so a disconnected or unpaired computer shows it (R7, R21) | As in slice 2 |
| `tervi connect` | As in slice 2; on a computer unpaired on the server, prints R21's message | As in slice 2 |

## Mechanisms

### One writer for `connect` and `stop_reason`

Applies to every change of `connect` and `stop_reason` in `state.json`.
Covers R1–R3, R9. Only the program holding `daemon.lock` writes them. A
running daemon holds it for its whole life. `tervi disconnect` first tries
to take the lock without waiting: if it gets it, no daemon is running and
none can start until it lets go, so it writes `connect: false` (keeping
every other field) and lets go; if it cannot, it calls `POST /disconnect`.
A daemon that starts afterwards reads `connect: false` and stays idle, also
after a reboot (R2).

### Reading the state without a daemon

Covers R7, R21. When `tervi status` finds no daemon, it reads `state.json`
before saying tervi is not running: `connect: false` with no stop reason
prints `○ Disconnected.`, and `credential_revoked` or `credential_rejected`
prints that reason's message. Only `connect: true` with no daemon prints
slice 2's "isn't running in the background". (`tervi connect` starts the
daemon as in slice 2, so it learns a revocation from its answer.)

### Checking a credential

Covers R11, R13, R18, R20. Every route that takes a credential hashes it
and reads that one row of `credentials` by its unique hash. `active` is
accepted; `revoked` is refused with `credential_revoked` (the unpair route
answers `204` instead, R12); no row, or `pending`, `failed`, or `expired`
on a route that needs `active`, is refused with `unknown_credential`. The
acknowledgment and save-failure routes keep slice 1's handling of
`pending`, `failed`, and `expired`.

### Ending a credential

Covers R11, R18, R26. Unpair, Revoke, and the acknowledgment of a pairing
again all end a credential the same way: in one transaction, the
`active` credential becomes `revoked` with `ended_at = now()`; after the
transaction commits, the server closes that machine's connection, if it has
one, with `4002`. Closing after the commit means a worker that reconnects at
once is already refused. A connection that was being opened at that moment
(its credential checked just before the commit, but not yet in the
server's list of connections) is caught too: right after the connection
route adds a connection to the list, it reads that credential's status
again and closes with `4002` if it is no longer `active`.

### Unpairing on the computer

Covers R10–R16. `tervi unpair`:

1. Looks at what is on the computer (R15). A usable `credential.json`
   (also from a pairing that never finished) is unpaired as below, after
   R10's question. Leftovers without a usable `credential.json` (missing,
   damaged, or readable by others) are removed without contacting the
   server, after R15's own question: steps 3–5 only. Nothing at all prints
   R15's "isn't paired".
2. Unless `--local-only`, sends `POST /api/v1/machines/current/unpair` once,
   waiting at most 10 seconds. `204` or `401` (any error code) means the
   server part is done (R11, R13). No answer, or any other answer, removes
   nothing and prints R12's message.
3. Stops and disables the service (`systemctl --user disable --now tervi`),
   then takes `daemon.lock`, waiting up to 10 seconds, so no daemon is left
   running, also one started by hand.
4. Removes the service file and runs `systemctl --user daemon-reload`,
   deletes `credential.json`, then any leftover `.credential-*` temporary
   files and `credential-check.json`, then `state.json`, and lets go of the
   lock.
5. Checks linger with `loginctl show-user` and prints R11's line if it is
   on.

A failure in steps 3–4 stops there and prints what could not be removed and
`Finish with: tervi unpair --local-only` (R16). Running the command again
after any failure is safe: the server answers `204` again, and every removal
treats an already missing file as done.

### Recognizing a computer that pairs again

Covers R23–R28. When pairing starts, the worker sends `identity_hash`
(HMAC-SHA256 keyed with `/etc/machine-id`, over tervi's app ID, as 64 hex
characters), or none if it has no machine ID and the user agreed (R23). The
server stores it on the pairing request and, if it is set, looks for
machines with the same `identity_hash` that have had an `active` or
`revoked` credential, taking the one whose latest `active` or `revoked`
credential was created last (R25: the most recently paired; a failed or
expired attempt does not count); it stores that machine as
`matched_machine_id`. The approval answer shows that machine:
`still paired` while it has an `active` credential, otherwise the
`ended_at` of its most recently ended credential (R24). On the
correct code, the new `pending` credential is attached to the matched
machine instead of a new one. On the acknowledgment, in one transaction,
the new credential becomes `active`, the machine's earlier `active`
credential (if any) is ended as above, and the machine's hostname, OS, and
display name are updated from the pairing request (R26). A pairing with no
match is as in slice 1 (R28).

### Moving existing machines

Applies once, in migration `0003`. Each existing machine gets one
`credentials` row with its credential hash, status, pairing request,
creation time, and expiry; then those columns are removed from `machines`.
Existing machines get no identity (`null`), so they are never matched; a
computer paired before this slice is recognized only after it pairs once
more.

## Technical decisions

- A computer's identity for recognizing it (R23) is a one-way hash of
  `/etc/machine-id` combined with a fixed tervi-specific value, so the same
  computer always gives the same identity, and the machine ID itself can't
  be worked back from it. It is computed as systemd does for an
  application-specific ID: HMAC-SHA256 keyed with the machine ID, over
  tervi's fixed 16-byte app ID, a constant in the worker's code that never
  changes. The worker computes it each time it pairs, sends only the
  result, and saves nothing; the server only stores and compares it, on
  each machine — it survives a
  rename and the loss of tervi's own files, and systemd's documentation asks
  applications to use an application-specific value derived from the
  machine ID instead of the confidential ID itself.
  Not chosen: a random ID that tervi creates and keeps in a file — lost
  whenever tervi's files are, which is the case recognition is most needed
  for; the raw `/etc/machine-id` — systemd treats it as confidential, and
  sending it lets other software link the computer across services.
- Only the program holding `$XDG_RUNTIME_DIR/tervi/daemon.lock` writes
  `connect` and `stop_reason` in `state.json`. A running daemon holds it, so
  `tervi disconnect` first tries to take the lock: if it gets it, no daemon
  is running and none can start, so it writes `connect: false` itself and
  lets go; if it cannot, it asks the running daemon (R1, R3) — there is
  still one writer at a time, and R3 forbids starting the daemon just to
  write. This replaces slice 0002's "only the daemon writes".
  Not chosen: starting the daemon to write it, then stopping it — breaks
  R3; a separate marker file such as `~/.config/tervi/disconnected` — two
  files would answer "should I connect?" and could disagree.
- `connect: false` with no `stop_reason` means the user disconnected; no
  new value is added. A computer paired in slice 0001 that never ran
  `tervi connect` is in the same state and also shows `○ Disconnected.`
  (R7) — that message is true for it too, and one state is less to keep
  correct.
  Not chosen: a new `stop_reason: "disconnected"` — keeps slice 0002's
  `○ Not connecting.` for that rare leftover case, at the cost of a third
  state.
- The server keeps the computer and its credentials apart: `machines` holds
  one row per computer (name, operating system, `last_seen_at`, identity),
  and a new table holds one row per pairing that issued a credential (its
  hash, status, expiry, and when it ended). Pairing again adds a new
  credential row; no credential goes back to `active` — while a recognized
  computer's pairing is finishing, one computer has its old `active` credential
  and its new `pending` one at the same time (R26), which one row cannot
  hold, and later slices' workspaces can point at the computer alone.
  Not chosen: one table, where pairing again creates a new row pointing to
  the old one — the computer's history is spread over a chain of rows that
  every later feature must follow; one table updated in place with a second
  credential column — brings back `revoked → active`, and every check must
  look at two credential columns.
- Whether a machine is paired is not stored on `machines`: a machine is
  paired exactly while it has an `active` credential, and at most one
  credential per machine can be `active` — the fact lives in one place, so
  the two tables can never disagree.
  Not chosen: a `status` column on `machines` as well — every unpair,
  revoke, and pairing again would have to update both tables, and a missed
  update could list a computer whose credential no longer works.
- The Revoke route takes no proof and has no `Origin` check (R30): any
  request that reaches the server can revoke a machine whose `machine_id`
  it names — the simplest route while there is no sign-in; the
  `machine_id` is random, and another website cannot read the Machines list
  to learn it. Sign-in comes before release, in a later slice.
  Not chosen: accepting only requests whose `Origin` equals the address the
  request was sent to — blocks a revoke sent by another website open in the
  user's browser, but the user chose to keep this route as simple as
  possible until sign-in; a token the page sends back — more code for the
  same protection as the `Origin` check.
- When a connected machine is revoked, the server closes its connection
  with a new close code, `4002` ("revoked") — the worker learns at once
  that it must stop, instead of reconnecting once only to be refused.
  Not chosen: an existing code such as `1001` — it would say something
  untrue ("the server is stopping"), and the worker would try once more
  before stopping.
- The server alone decides why a credential is refused, from that
  credential's one row (found by its unique hash): `revoked` answers
  `401 {"error": "credential_revoked"}`; no row, or any other status that
  is not `active`, answers `401 {"error": "unknown_credential"}` as in
  slice 0002. `credential_revoked` and the close code `4002` mean the same,
  so a computer that was offline when it was revoked learns the same reason
  on its next try as one that was connected (R20, R21).
  Not chosen: one answer, `unknown_credential`, for every refused
  credential — a revoked computer that was offline would show slice 0002's
  message while a connected one, closed with `4002`, would show R20's;
  dropping `4002` too and showing slice 0002's message in every case —
  consistent and simpler, but the computer could no longer say what
  happened, and R20–R22 would change.
- A credential that stopped working has one status, `revoked`, whether
  `tervi unpair` or the Revoke button ended it — the result on the server
  is the same, so one value represents it; the two differ only in what the
  computer does, which the server does not record.
  Not chosen: separate `unpaired` and `revoked` statuses — two values for
  the same state, kept only to show on the approval page which way a
  pairing ended.
- `tervi unpair` tells the server with
  `POST /api/v1/machines/current/unpair` and
  `Authorization: Bearer <credential>`, tried once. An `active` credential
  becomes `revoked`, the machine's connection is closed if open, and the
  answer is `204`; an already `revoked` credential answers `204` again, so
  running the command again after a lost answer finishes it (R12) — it
  works whether or not the computer is connected, and uses the proof and
  the `/api/v1/machines/current/…` routes of slice 0002.
  Not chosen: an `unpair` message over the WebSocket — after
  `tervi disconnect` there is no connection, and R3 forbids opening one;
  no server step at all — that is `--local-only` (R14); answering a repeat
  with `401 credential_revoked` — it also finishes (R13), but a normal
  retry would go through the path for refusals.
- The match is decided once, when the pairing starts, and stored on the
  pairing request as `matched_machine_id`; the approval page shows that
  machine, and the correct code attaches the new credential to that same
  machine (R24–R26) — what the user saw is what Accept does, even if
  another pairing of the same computer finishes in the minutes between.
  Not chosen: looking the match up again when the code arrives — nothing
  new to store, but the page could show one machine while Accept attaches
  the computer to another.
- `state.json`'s `stop_reason` gains the value `"credential_revoked"`,
  written by the daemon when the server closes with `4002` or answers
  `401 credential_revoked`; `"credential_rejected"` stays for
  `unknown_credential` (R20, R21) — the same field and pattern as slice
  0002, and each reason keeps its own message.
  Not chosen: `"credential_rejected"` for both — `tervi status` could not
  tell them apart, so R21's message could not be shown.
- `tervi unpair` (also `--local-only`) deletes, after `credential.json`,
  every leftover `.credential-*` temporary file and `credential-check.json`
  in `~/.config/tervi/` — a crash while saving can leave a temporary file
  holding the credential, and after unpairing no copy of the secret should
  remain on the computer.
  Not chosen: leaving them — they are `0600`, but a copy of the secret
  would stay behind without the user knowing.

## Requirement coverage

| Requirement | Tasks |
| --- | --- |
| R1 | 05, 08 |
| R2 | 05, 08 |
| R3 | 05 |
| R4 | 05 |
| R5 | 05 |
| R6 | 05 |
| R7 | 04, 05, 08 |
| R8 | 05, 08 |
| R9 | 05, 08 |
| R10 | 06 |
| R11 | 02, 06, 08 |
| R12 | 02, 06 |
| R13 | 02, 06 |
| R14 | 06 |
| R15 | 06 |
| R16 | 06 |
| R17 | 02 |
| R18 | 01, 02, 08 |
| R19 | 01, 02 |
| R20 | 01, 04, 08 |
| R21 | 04, 05, 08 |
| R22 | 07 |
| R23 | 03, 07 |
| R24 | 03 |
| R25 | 03 |
| R26 | 03, 08 |
| R27 | 03 |
| R28 | 03 |
| R29 | 03, 07 |
| R30 | 02 |

R8 and R9 need no new server code: a disconnect closes the connection with
`1000`, and slice 2 already shows such a machine Offline and lets
`tervi connect` connect again. Tasks 05 and 08 prove they still hold.

## Tasks

| # | Task | Depends on | Risk | Status |
| --- | --- | --- | --- | --- |
| 01 | [Server: credentials in their own table](tasks/01-server-credentials.md) | — | core | done |
| 02 | [Server: unpair and Revoke, and the Machines page's Revoke button](tasks/02-server-unpair-revoke.md) | 01 | core | todo |
| 03 | [Server: recognizing a computer that pairs again, and the approval page](tasks/03-server-recognition.md) | 01, 02 | core | todo |
| 04 | [Worker: a revoked credential, and the state without a daemon](tasks/04-worker-revoked.md) | 01 | core | todo |
| 05 | [Worker: `tervi disconnect`](tasks/05-worker-disconnect.md) | 04 | core | todo |
| 06 | [Worker: `tervi unpair`](tasks/06-worker-unpair.md) | 02, 05 | core | todo |
| 07 | [Worker: the computer's identity when pairing](tasks/07-worker-identity.md) | 03, 04, 06 | core | todo |
| 08 | [End-to-end test of disconnect, unpair, Revoke, and pairing again](tasks/08-end-to-end.md) | 01–07 | normal | todo |

The server tasks (01, 02, 03) and the worker tasks (04, 05, 06, 07) each run
one after another, because the tasks on each side change some of the same
files (the contract test and the web tests; `cli.go` and `cmd/tervi`).
Once 01 is in, the two sides can be built at the same time: 02 next to 04,
03 next to 05.

Tasks 04 and 07 are smaller than the usual 300–400 lines. 04 stays separate
so the daemon's handling of a revocation is reviewed on its own and can be
built next to task 02; merged into 05, that task would grow past 400 lines.
07 stays separate because it is the only task that reads `/etc/machine-id`,
which systemd treats as confidential, so its review can focus on that.
