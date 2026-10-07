# 0001 Pairing — plan

Status: draft
Spec: [spec.md](spec.md)

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| Server program | `cmd/server` | A thin wrapper around `internal/server` |
| Server assembly | `internal/server` | Settings, connecting, migrating, starting the clean-up, and `New(...)`, which returns the server's `http.Handler` with every route. Importable, so the end-to-end test runs the real server. |
| Database | `internal/db` | Connection and migrations: embedded `.sql` files run with `github.com/pressly/goose/v3` at server start |
| Secrets | `internal/secret` | The `[hidden]` secret type; generating keys, credentials, and pairing codes; hashing |
| Pairing on the server | `internal/pairing` | Pairing requests, machines, the status changes, their HTTP handlers, and `Register(mux, ...)` for its routes |
| Approval page | `internal/web` | `index.html`, `app.js`, and `view.js`, embedded with `go:embed`; no build step; `Register(mux)` for its routes |
| Worker program | `cmd/tervi` | A thin wrapper around `local.Run` |
| Worker command and local state | `internal/worker/local` | The command line, the address standard form, `state.json`, the secret store entry, step 0, and `Run(ctx, args, stdin, stdout, stderr, store, stateDir, flow)`, importable so the end-to-end test runs the real worker |
| Worker pairing flow | `internal/worker/flow` | Steps 1–7b as seen from the worker, Ctrl+C, every terminal message |
| End-to-end test | `test/e2e` | Runs the real server and worker together |

Each server package registers its own routes, so tasks that add routes do not
edit the same file.

## Data

Column types and constraints are in task 01, which creates both tables.

### Pairing request

One record per pairing attempt. Table `pairing_requests`.

| Field | Meaning |
| --- | --- |
| `id` | The pairing request's ID |
| `hostname`, `os_name`, `os_version` | As reported by the worker; each one may be empty |
| `status` | Lifecycle state (below) |
| `polling_key_hash` | Hash of the polling key |
| `approval_key_hash` | Hash of the approval key |
| `pairing_code` | Created when the user accepts. Stored as it is, so the page can show the same code again (R11, R22). |
| `tries_left` | Wrong codes still allowed. Starts at 5. |
| `failure_reason` | Set whenever the status becomes `failed`: `wrong_codes`, `not_saved`, or `not_confirmed`. Empty otherwise. |
| `expires_at` | 10 minutes after creation |

### Pairing request lifecycle

```text
waiting_for_approval ───Pair───► waiting_for_code ──correct code──► finishing ──acknowledged──► paired
        │                              │                               │
        ├──Reject──► rejected          ├──tries_left reaches 0──► failed ◄──saving failed, or machine expired
        └──expires_at passes──► expired ◄──────────── expires_at passes
```

### Machine

One record per paired computer. Table `machines`. Created when the correct
code arrives; a pairing request holds only the pairing state, never the
credential.

| Field | Meaning |
| --- | --- |
| `machine_id` | The machine's permanent ID |
| `pairing_request_id` | The pairing request that created this machine |
| `hostname`, `os_name`, `os_version` | As reported during pairing |
| `display_name` | Starts as the hostname; editing it comes later |
| `credential_hash` | Hash of the credential; the credential itself is never stored |
| `status` | Lifecycle state (below) |
| `expires_at` | 5 minutes after creation; matters only while `pending` |

### Machine lifecycle

```text
pending ──acknowledged──► active
   ├──worker reports saving failed──► failed
   └──expires_at passes──► expired
```

Only `active` machines exist for the user. A `pending`, `failed`, or `expired`
machine appears nowhere the user can see, and its credential is accepted for
nothing except acknowledging or reporting a failure while `pending`.

### Worker secret store entry

One entry in the OS secret store (GNOME Keyring on Linux), named
service `tervi`, user `worker`, accessed with the Go library
`github.com/zalando/go-keyring`. Its value holds the credential together with
the server it belongs to:

```json
{ "server": "http://localhost:8080", "credential": "<credential>" }
```

**This entry is the only place the worker reads the server address from
before sending the credential.** The credential is sent only to the server
saved with it (R30).

### Worker state

A small file on the paired computer: `~/.config/tervi/state.json`. It holds
nothing secret and no server address, so editing it cannot redirect the
credential.

| Field | Meaning |
| --- | --- |
| `machine_id` | The machine this computer became |
| `credential_saved` | `false` from just before the secret store entry is saved; `true` once it was saved and read back |
| `confirmed` | `true` once the server answered OK to the acknowledgment |

`state.json` is always written **before** the step it describes, so a stop at
any moment leaves a note saying what was about to happen (step 0 reads it).

### Names

| Name | What it is |
| --- | --- |
| Pairing request | The record of one pairing attempt |
| Approval key | The random key in the approval link `/pair/<approval key>` |
| Polling key | The worker's own key; it polls and submits the code with it |
| Pairing code | The code the browser shows and the user types into the terminal |
| Credential | The permanent proof the worker receives at the end |

## Worker command

The only command in this slice is `tervi pair --server <url>`. Covers R1.

| Input | Output | Exit code |
| --- | --- | --- |
| `tervi pair --server <valid url>` | Starts pairing (step 1) | `0` paired, `1` any failure |
| `--server` missing | `Usage: tervi pair --server <url>` | `2` |
| Invalid server address | `Invalid server address: <value>` + usage | `2` |
| Unknown flag | `Unknown flag: <flag>` + usage | `2` |
| Unknown command | `Unknown command: <command>` + usage | `2` |
| No command | Usage only | `2` |
| Ctrl+C at any point | See "Cancelling with Ctrl+C" | `130` |

**A valid server address** starts with `http://` or `https://` and has a host,
with an optional port: `http://localhost:8080`. Nothing else is allowed except
a single trailing `/`: a path, a query (`?`), a fragment (`#`), or a user name
makes the address invalid, because the worker always calls the API at the
server's root. An invalid address stops the command before anything is
contacted.

## Server setup

| Setting | Value in this slice | Why |
| --- | --- | --- |
| `SERVER_ADDR` (where the server listens) | `127.0.0.1:8080` by default | Only this computer can reach the server, which keeps R21's limitation safe. The current default `:8080` listens on every network interface and must change. **The server refuses to start if the host is not a loopback address** (`127.0.0.1`, `::1`, or `localhost`), with a message saying that sign-in must exist first. |
| `TERVI_PUBLIC_URL` (the address in approval links) | `http://localhost:8080` | See step 1. A trailing `/` is removed, so links never contain `//pair/`. |

## Interfaces

Every proof (polling key, approval key, credential) is sent in the
`Authorization: Bearer <proof>` header, never in a URL or a body. The only
exception is the approval page's own address, `/pair/<approval key>`, which
the page reads from the address bar and then sends as a header. Every API
answer is JSON, and every error answer is `{"error": "<code>"}`.

| # | Method and path | Called by | Proof | Purpose |
| --- | --- | --- | --- | --- |
| 1 | `POST /api/v1/pairings` | Worker | None | Start a pairing (step 1) |
| 2 | `GET /api/v1/pairings/current` | Worker | Polling key | Poll (step 2) |
| 3 | `POST /api/v1/pairings/current/code` | Worker | Polling key | Submit the code; receive the credential (steps 5–6) |
| 4 | `POST /api/v1/machines/current/acknowledgment` | Worker | Credential | Confirm the credential was saved (step 7b) |
| 5 | `POST /api/v1/machines/current/save-failure` | Worker | Credential | Report that saving failed (step 7a) |
| 6 | `GET /pair/{approval_key}` | Browser | Approval key in the path | The approval page itself (HTML) |
| 7 | `GET /api/v1/approvals/current` | Browser | Approval key | The page's data, also used for polling (step 3) |
| 8 | `POST /api/v1/approvals/current/accept` | Browser | Approval key | The **Pair** button (step 4) |
| 9 | `POST /api/v1/approvals/current/reject` | Browser | Approval key | Reject (step 4) |

**1 — Start.** Request: `{"hostname": "...", "os_name": "...", "os_version": "..."}`,
each field optional. Answer `201`:
`{"polling_key": "...", "approval_url": "http://localhost:8080/pair/...", "expires_in_seconds": 600}`.
A value over its limit: `400 {"error": "invalid_input"}`.

**2 — Poll.** Answer `200`: `{"status": "...", "expires_in_seconds": 412}`, plus
`"tries_left": 5` when the status is `waiting_for_code`. The status is the
current one after expiry is applied (see Mechanisms): `waiting_for_approval`,
`waiting_for_code`, `finishing`, `paired`, `rejected`, `failed`, or `expired`.
An unknown polling key: `401 {"error": "unknown_key"}`.

**3 — Code.** Request: `{"code": "4827-1934"}`. Answer `200` with one `result`:

| `result` | Other fields | Meaning |
| --- | --- | --- |
| `accepted` | `credential`, `machine_id` | Correct code; step 6 done |
| `wrong_code` | `tries_left` | Wrong code, tries remain |
| `failed` | — | Wrong code, no tries left |
| `expired` | — | The pairing expired |
| `not_waiting_for_code` | `status` | The pairing is in another state, for example `finishing` after an earlier correct code |

An unknown polling key: `401 {"error": "unknown_key"}`. A body over 4 KB:
`400 {"error": "invalid_input"}`. Expiry applies only to waiting statuses: a
`rejected` or `failed` request answers `not_waiting_for_code` with its status,
however old it is.

**4 — Acknowledgment** and **5 — Save failure.** No body. Answer `200` with one
`result`:

| Machine | 4 — Acknowledgment | 5 — Save failure |
| --- | --- | --- |
| `pending`, not expired | `ok` (now `active`) | `ok` (now `failed`) |
| `active` | `ok`, nothing changes (safe to repeat) | `active`, nothing changes |
| `failed` | `failed` | `failed` |
| `expired`, or `pending` past `expires_at` | `expired` | `expired` |

An unknown credential: `401 {"error": "unknown_credential"}`.

**7, 8, 9 — Approval data.** Answer `200`:
`{"status": "...", "hostname": "...", "os_name": "...", "os_version": "...", "already_decided": false}`,
plus `"pairing_code"` while `waiting_for_code`, `"failure_reason"` when `failed`,
and `"display_name"` when `paired`. For 8 and 9, `"already_decided": true` means
another click decided first; the rest of the answer is the real state. An
expired request answers `expired` with `"already_decided": false`, since no
click decided it. An unknown approval key: `404 {"error": "invalid_link"}`.

## Flow

### Step 0 — The worker reads its state

Before contacting the server, the worker reads `state.json` and its secret
store entry. "Saved server" below always means the server in the secret store
entry, compared with `--server` in standard form (see Mechanisms).

| `state.json` | Secret store entry | Worker does |
| --- | --- | --- |
| Missing | Missing | Checks that the secret store is usable (see Mechanisms), then starts a new pairing (step 1) |
| Missing | Exists | Incomplete data: stop, change nothing (R31). Since `state.json` is always written first, only something outside tervi can cause this. |
| `credential_saved: false` | Missing | The save never happened, so the credential is lost. Deletes `state.json` and shows `✗ The earlier pairing was interrupted before the credential was saved. Run the same command again to start a new one.` The server's machine expires by itself. |
| `credential_saved: false` | Exists | The save happened but was not recorded. Checks the entry is well formed (below), sets `credential_saved: true`, then continues as in the next row. |
| `credential_saved: true`, `confirmed: false` | Exists | `--server` equals the saved server: finishes the earlier pairing by sending the acknowledgment again (step 7b). `--server` differs: refuses and changes nothing: `✗ A pairing with <saved server> isn't finished yet.` followed by `To finish it, run:  tervi pair --server <saved server>` |
| `credential_saved: true` | Missing | Incomplete data: stop, change nothing (R31) |
| `confirmed: true` | Exists | Already paired (R2): `This computer is already paired with <saved server>. Nothing was changed.` Exit code `1`. |

**Incomplete data** gets a message naming what is missing, and a hint for
cleaning up by hand until `tervi unpair` exists:

```text
✗ The local pairing data is incomplete: state.json exists, but the secret store entry is missing.
  Nothing was changed.
  To start over, delete ~/.config/tervi/state.json and the "tervi" entry in Passwords and Keys.
```

**A well-formed entry** is valid JSON with a `server` in standard form and a
non-empty `credential`. Whenever step 0 finds an entry that is not well
formed, it stops, changes nothing, and exits with `1`:

```text
✗ The secret store entry for tervi is damaged. Nothing was changed.
  To start over, delete ~/.config/tervi/state.json and the "tervi" entry in Passwords and Keys.
```

**A credential only ever goes to the server that issued it.** That is why a
different `--server` is refused instead of used. Once `tervi unpair` exists
(a later slice), this message will also offer unpairing.

**When finishing an earlier pairing**, the server's answer decides:

- OK → `confirmed: true`, and `✓ Paired successfully.`
- The machine expired, failed, or is unknown (`401`) → the credential will
  never work, so the worker deletes `state.json` and its secret store entry,
  and shows one of these, then exits with `1`:

| Answer | Message |
| --- | --- |
| `expired` | `✗ The earlier pairing didn't finish in time. Run the same command again to start a new one.` |
| `failed` | `✗ The earlier pairing failed. Run the same command again to start a new one.` |
| `401` | `✗ The server doesn't recognize the earlier pairing. Run the same command again to start a new one.` |

During a first run (step 7b), the same three cases use the same messages
without the word "earlier".

### Step 1 — The worker starts a pairing

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server |
| Endpoint | `POST /api/v1/pairings` |
| Sends | `hostname` (≤ 64), `os_name` (≤ 64), `os_version` (≤ 32). Each one is optional. |
| Server does | Creates a pairing request with status `waiting_for_approval` and `expires_at` 10 minutes later. Generates a polling key and an approval key and stores only their hashes. |
| Server answers | The polling key, the approval link (`<public address>/pair/<approval key>`), and `expires_in_seconds` (600 at the start) |
| Worker then | Shows the computer's details, the link, and the expiry as a clock time in its own local time (R5), then polls (step 2) |
| If the worker cannot connect | No connection within 10 seconds (wrong address, server not running, no network). The worker does not retry. It shows `✗ Can't reach <server>. Check that the server is running, then run the command again.` and stops with exit code `1` (R4). |
| If the request times out after it was sent | The worker does not retry. It says the pairing could not be started and the user can run the command again. A pairing the server did create expires by itself. |

**The approval link's address** comes from the server's own setting,
`TERVI_PUBLIC_URL`: the address other devices use to reach the server. It is
never taken from the request. The worker shows the link exactly as received.
In this slice the public address is `http://localhost:8080`.

**The worker's deadline** is its own clock at the moment the answer arrived,
plus `expires_in_seconds`. The server sends a duration rather than a time of
day, so a wrong clock on either computer cannot move the worker's deadline.
While the server can be reached, the server's own answer (`expired`) decides.

**Values the worker cannot read** are left out. The terminal and the browser
show `unknown` in their place.

**Values longer than their limit** are truncated by the worker: a value that
fits is kept whole; a longer one keeps its first (limit − 1) characters and
ends with `…`, so the result is exactly at the limit. Limits count characters,
not bytes. Examples for a limit of 64: 64 characters stay unchanged; 65 become
63 characters + `…`.

**The server rejects** any value over its limit with "invalid input". The real
worker never sends one; the check protects against other callers.

### Step 2 — The worker polls

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server, repeated |
| Sends | The polling key |
| Server does | Finds the pairing request by the polling key's hash and answers with its status |
| Worker then | `waiting_for_approval`: poll again. `rejected` or `expired`: say so and stop. `waiting_for_code`: stop polling and ask for the code (step 5); the answer includes `tries_left` and `expires_in_seconds`, and the worker resets its internal deadline from it. The **expiry time shown** to the user stays the one computed at step 1, so the prompt shows exactly the same time again (R12). |
| Covers | R9, R24 |

**When to poll.** The next poll starts 2 seconds after the previous one ended.
A poll ends when an answer arrives, when it fails at once (for example,
connection refused), or when 10 seconds pass without an answer. So polls never
overlap, and a failing server is not hammered. Polling only reads, so repeating
it is always safe.

**What the terminal shows:**

| Moment | Terminal | Then |
| --- | --- | --- |
| Polling starts | `Waiting for approval... (expires at 14:32)` | Poll |
| First failed poll: no answer, connection error, `5xx`, or an answer that is not valid JSON | `Connection lost, retrying...` (once, not on every failure) | Keep polling |
| First successful poll after failures | `Connection restored.` | Keep polling |
| `rejected` | `✗ Pairing was rejected in the browser.` | Stop, exit code `1` |
| `expired`, or the worker's deadline passes while the server cannot be reached | `✗ The link expired. Run the command again.` | Stop, exit code `1` |
| `waiting_for_code` | `✓ Approved in the browser.` then `Type the code shown in the browser (expires at 14:32, 5 tries left):` | Step 5 |
| `401 unknown_key` | `✗ The server no longer knows this pairing. Run the command again.` | Stop, exit code `1` |
| Any other status or answer (`finishing`, `paired`, `failed`, another `4xx`) | `✗ Unexpected answer from the server. Run the command again.` | Stop, exit code `1` |

### Step 3 — The user opens the approval link

| Question | Answer |
| --- | --- |
| Who → who | Browser → Server |
| Request | `GET`, with the approval key. Opening the page changes nothing (R6). |
| Server answers | `hostname`, `os_name`, `os_version`, and the status |
| Browser then | Shows the computer's details with **Pair** and **Reject** |

Sign-in before this step comes in a later slice.

### Step 4 — The user accepts or rejects

| Question | Answer |
| --- | --- |
| Who → who | Browser → Server, with the approval key |
| Reject | Status becomes `rejected`. The worker learns it at its next poll. |
| Pair (the accept action) | Status becomes `waiting_for_code`. The server creates the pairing code, saves it, and the page shows it and asks the user to type it into the terminal. |

### The approval page stays up to date

The page cannot learn about changes by itself, so while it shows the buttons
or the pairing code, it **polls**: it repeats step 3's `GET` 2 seconds after
the previous poll ended. It stops polling once it shows a result. Covers R13,
R19.

| Status | The page shows |
| --- | --- |
| `waiting_for_approval` | The computer's details with **Pair** and **Reject** (keeps polling) |
| `waiting_for_code` | The pairing code, and "Type this code into the terminal of bert-desktop" (keeps polling) |
| `finishing` | Code accepted. Finishing on bert-desktop… (keeps polling) |
| `paired` | ✓ **bert-desktop is paired.** Ubuntu 26.04. You can close this tab. |
| `rejected` | Rejected. This computer was not paired. |
| `expired` | ✗ **This link has expired.** Run `tervi pair` on the computer again to get a new link. |
| `failed`, `wrong_codes` | ✗ **Pairing failed: the wrong code was typed 5 times.** Run `tervi pair` on the computer again to start over. |
| `failed`, `not_saved` | ✗ **Pairing failed: the computer couldn't save its credential.** Check the terminal on bert-desktop for details. |
| `failed`, `not_confirmed` | ✗ **Pairing failed: the computer didn't confirm in time.** Run `tervi pair` on the computer again. |

Every failure uses the same red error style, with a sentence that names the
cause and the next action. The `paired` result shows the machine's display
name, OS name, and OS version.

### Step 5 — The worker submits the pairing code

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server |
| Sends | The pairing code the user typed, and the polling key |
| Code matches | Step 6 |
| Code does not match | `tries_left` goes down by 1. The server answers with the new `tries_left`, and the worker shows it. At 0 the status becomes `failed` and the worker stops. |
| Server answers `expired` | `✗ The code expired. Run the command again.` Stop, exit code `1`. |
| Worker's own deadline passes while the prompt is open | The same message. The worker stops waiting for input and stops, exit code `1`. |
| Server answers `not_waiting_for_code` | `✗ The pairing is no longer waiting for a code. Run the command again.` Stop, exit code `1`. |
| Server answers `401 unknown_key` | `✗ The server no longer knows this pairing. Run the command again.` Stop, exit code `1`. |
| Server answers `5xx`, or an answer that is not valid JSON | Treated as "no answer" (next row): the code may have been accepted. |
| Any other answer | `✗ Unexpected answer from the server. Run the command again.` Stop, exit code `1`. |
| No answer after sending the code (10 seconds, or the connection fails) | The worker does not send the code again: if the code was correct, the credential is lost, and the server never issues a second one. It shows `✗ No answer from the server after sending the code. Run the same command again to start over.` and stops, exit code `1`. Nothing was saved, so the next run starts a new pairing; a pending machine expires on the server. |
| Covers | R12, R13, R15 |

**The worker keeps its own deadline while it waits for the code.** It no
longer polls, so it cannot hear "expired" from the server until the user
submits a code. The deadline comes from `expires_in_seconds` in the approval
answer (step 2), so it is fresh and does not depend on the worker's clock
being right.

**Only the server counts tries.** The worker shows the number the server sends
and never counts by itself, so the two can never disagree, for example after
a lost response or a restarted terminal.

### Step 6 — The server issues the credential

| Question | Answer |
| --- | --- |
| When | Step 5's code matches |
| Server does | Creates a credential and a machine with status `pending` and `expires_at` 5 minutes later, storing only the credential's hash. The pairing request's status becomes `finishing`. |
| Server answers | The credential and the `machine_id` |
| Worker then | Saves them in this order (R18), then step 7a or 7b |

**The order of the worker's writes**, each one done before the next starts:

1. `state.json` ← `machine_id`, `credential_saved: false`, `confirmed: false`
2. Secret store entry ← the credential and the server address; then read it back
3. `state.json` ← `credential_saved: true`

A stop between any two of them is recovered by the next run (step 0).

**The server never keeps the credential in plain form, not even to send it
again.** If the answer is lost, the worker never has the credential, the
machine expires, and the user pairs again.

### Step 7a — Saving failed

| Question | Answer |
| --- | --- |
| Worker shows | `✗ Couldn't save the credential. Pairing was not completed.` (R20) |
| Worker sends | "Saving failed", with the credential as proof (once; its answer changes nothing) |
| Worker cleans up | Deletes the secret store entry if one was written (a save can write the entry and still fail its read-back), then deletes `state.json`. If the entry cannot be deleted, it also shows `✗ Couldn't remove the partly saved secret store entry.` and the manual clean-up hint, and keeps `state.json`. Exit code `1`. |
| Server does | The machine's status becomes `failed`; the pairing request's status becomes `failed`, and the browser shows that pairing failed |

This report may never arrive, for example when the network is down or the
worker crashed. The machine's 5-minute expiry covers that case.

### Step 7b — Saving succeeded

| Question | Answer |
| --- | --- |
| Worker sends | The acknowledgment, with the credential as proof |
| Server does | The machine's status becomes `active`; the pairing request's status becomes `paired`. The server answers OK. |
| Worker then | Updates `state.json` to `confirmed: true` and shows `✓ Paired successfully.` |
| Browser shows | The approval page changes to the `paired` result: `✓ bert-desktop is paired. Ubuntu 26.04. You can close this tab.` (R19) |

**The acknowledgment is safe to repeat.** If it arrives again for a machine
that is already `active`, with the same credential, the server answers OK
again and changes nothing. A repeat cannot create anything, so retrying it is
safe (unlike step 1).

**When the answer does not arrive**, the worker retries: 3 tries in total,
2 seconds apart, and shows each one:

```text
✓ Credential saved.
Confirming with the server...
  No answer, retrying (2 of 3)...
  No answer, retrying (3 of 3)...
✗ Saved, but couldn't confirm with the server.
  To finish, run:  tervi pair --server http://localhost:8080
```

The message always prints the full command with the saved server address, so
the user can copy it.

`state.json` then still says `confirmed: false`, and the next run finishes
the pairing (step 0).

### Cancelling with Ctrl+C

Covers R23. The worker stops at once; it does not tell the server, and an
unfinished pairing expires on its own. What it says depends on what was
already written:

| Ctrl+C during | Message | Exit code |
| --- | --- | --- |
| Step 0 to step 5: starting, polling, the code prompt | `Pairing cancelled. Nothing was saved.` | `130` |
| From step 6's first write onward | `Pairing cancelled before it was confirmed.` followed by `To finish, run:  tervi pair --server <server>`, where `<server>` is `--server` in standard form (the same address the entry holds or will hold) | `130` |

Ctrl+C never needs to wait: whatever was written, the next run knows what to
do (step 0).

## Mechanisms

Methods that apply to several steps.

### Expiry

Applies to: pairing requests (10 minutes) and pending machines (5 minutes).
Covers: R15, R16, R26.

Two separate jobs:

1. **Enforcing (at every request).** Before it reads or changes a record, the
   server compares `expires_at` with the current time. Only these count as
   expired, even if the stored status has not caught up yet:
   - a pairing request in `waiting_for_approval` or `waiting_for_code` past its
     `expires_at` reads as `expired`;
   - a machine in `pending` past its `expires_at` reads as `expired`, and its
     pairing request, in `finishing`, reads as `failed` with `not_confirmed`.

   Changes are refused in the same cases. Records in any other status
   (`paired`, `rejected`, `failed`, `active`, …) never expire. This check
   alone keeps R26 true at every moment.
2. **Cleaning up (every minute).** A background job writes those same results
   into the stored statuses, using conditional updates.

The clean-up only makes the stored status truthful. If it runs late or stops,
nothing unsafe happens, because the check at every request (point 1) already
refuses expired records.

All times come from the database's clock, both when `expires_at` is set and
when it is compared, so one clock decides.

### Status changes

Applies to: every change of a pairing request's or a machine's status, and to
`tries_left`. Covers: R10, R13, R26, and "never a second credential".

**Change first, in one step; ask why only if nothing changed.**

1. One `UPDATE` changes the record **only if it is still in the expected
   state** and not past its `expires_at`, and returns the changed row:

   ```sql
   UPDATE pairing_requests
      SET status = 'rejected'
    WHERE approval_key_hash = $1
      AND status = 'waiting_for_approval'
      AND expires_at > now()
   RETURNING status;
   ```

2. **A row came back:** this request made the change. Answer with the new state.
3. **Nothing came back:** something else changed it first, or it expired, or
   it never existed. Only now read the record, and answer with what is really
   there (`Already accepted`, `Already rejected`, `This link has expired`), or
   `Invalid link` if no record has that key.

The database lets only one of two simultaneous requests match the condition,
so two clicks, two tabs, or a double-click can never both win. Reading first
and changing afterwards would be a race: both requests could read "waiting"
before either one writes.

Where it is used:

| Step | Change | Only if |
| --- | --- | --- |
| 4 | Pair → `waiting_for_code`; Reject → `rejected` | status is `waiting_for_approval` |
| 5 | Wrong code: `tries_left` − 1, and `failed` with `wrong_codes` when it reaches 0 | status is `waiting_for_code` and `tries_left` > 0 |
| 6 | Correct code → `finishing`, and the machine is created | status is `waiting_for_code` |
| 7a | Machine → `failed`; pairing request → `failed` with `not_saved` | machine is `pending` |
| 7b | Machine → `active`, pairing request → `paired` | machine is `pending` |
| Expiry clean-up | Machine → `expired`; pairing request → `failed` with `not_confirmed` | machine is `pending` |

Step 6's condition is why the server can never issue a second credential: once
one correct code moved the request to `finishing`, another submission finds
nothing to change.

### Secret store access

Applies to: every read, save, and delete of the worker's secret store entry.
Covers: R3, R18, R20.

**Every operation is checked, and every failure is shown to the user.** No
secret store failure is ever silent. The worker names the operation that
failed, adds a hint, and stops with exit code `1`:

```text
✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.
  Make sure you are logged in to a desktop session and the keyring is unlocked.
```

| Where | Operation | If it fails |
| --- | --- | --- |
| Before step 1 (R3) | Save a test value, read it back, delete it | Stop before contacting the server |
| Step 0 | Read the entry | Stop |
| Step 6 | Save the real entry, read it back, compare | Saving failed: step 7a |
| Step 0, cleaning up an expired pairing | Delete the entry | Stop |

**A save counts as successful only if reading it back returns exactly what
was saved.**

### Secret values

Applies to: the approval key, the polling key, the credential, and the pairing
code. Covers: R14, R22.

| Secret | Made of | Stored on the server as |
| --- | --- | --- |
| Approval key, polling key, credential | 32 random bytes from Go's `crypto/rand`, written as base64url without padding (43 characters) | SHA-256 hash (`bytea`, unique) |
| Pairing code | 8 random digits from `crypto/rand`, shown as `4827-1934` | As it is (R22) |

SHA-256 is the right hash here, not bcrypt: bcrypt is slow on purpose to
protect weak human passwords, while these values are random and impossible to
guess. Hashes are compared in constant time.

When a code is checked, `-` and spaces are ignored, so `48271934` and
`4827 1934` match `4827-1934`. A code is only ever checked against the pairing
request found by the polling key (R14).

### Secrets never printed

Applies to: the approval key, the polling key, the pairing code, and the
credential, in both the worker and the server. Covers: R18, R22.

Each secret is held in its own small Go type, whose printed form is always
`[hidden]`, never its value. The value is stored **behind a pointer** inside
the type: Go's printing reaches into a struct's fields without asking the
type, so a plain string field would leak when the secret sits inside another
struct; behind a pointer it prints only an address. So printing a value,
logging an error, or dumping a whole answer can never reveal a secret by
accident. The value is read only where it is really needed: sending it,
hashing it, or saving it in the secret store.

**One deliberate exception:** the worker prints the approval link, which
contains the approval key (R5). That is its purpose. The approval key appears
nowhere else in the worker's output, and nowhere in the server's logs. The
server never logs a request path under `/pair/`.

A test runs the full pairing flow, captures everything printed and logged by
the worker and the server, and fails if any secret's value appears in it,
apart from the approval key inside the printed link.

### Server address comparison

Applies to: comparing `--server` with the saved server (step 0). Covers: R30.

Two addresses can look different and mean the same server, such as
`http://LOCALHOST:8080/` and `http://localhost:8080`. Both are first put into
a standard form, then compared exactly:

- scheme and host in lowercase;
- the default port removed (`:80` for `http`, `:443` for `https`);
- no trailing `/`.

The worker saves the standard form in its secret store entry.

## Technical decisions

- Proofs travel in the `Authorization: Bearer` header — URLs and bodies end
  up in logs and browser history far more often than headers.
- Secrets are 32 random bytes, hashed with SHA-256 — impossible to guess, so
  a fast hash is safe; a slow password hash would only slow down every poll.
- The pairing code is 8 digits, and `-` and spaces are ignored — easy to read
  and type; with 5 tries out of 100 million codes, guessing is hopeless.
- Migrations are embedded `.sql` files run by `goose` at server start — the
  usual Go approach; the schema travels with the program.
- The approval page is plain HTML and JavaScript embedded in the server — it
  is one small page; the React and Theia setup comes with later features.
- The server listens on `127.0.0.1` only — without sign-in, anyone who can
  reach the server can pair (R21); listening only on this computer makes that
  limitation safe instead of just written down.
- One command, `tervi pair --server <url>`; anything else is wrong usage —
  slice 1 needs nothing more, and a clear "Unknown flag" beats a silent default.
- Exit codes `0` paired, `1` failure, `2` wrong usage, `130` cancelled with
  Ctrl+C — scripts can tell them apart; finer codes can come later.
- The approval link uses the server's configured public address, never the
  worker's `--server` value or the request's `Host` header — the worker and a
  phone often need different addresses for the same server, and a request's
  `Host` header can be forged to point links at another site.
- Resource-style paths (`POST /api/v1/pairings` creates a pairing) — the common
  REST convention; every endpoint in this plan follows the same style.
- The hostname is the name shown to the user — custom names come later.
- Hostname and OS name up to 64 characters, OS version up to 32 — 64 is the
  longest hostname Linux allows, and real OS names and versions are far shorter.
- Polling every 2 seconds, counted from the end of the previous poll — R9's
  "within a few seconds" holds, polls never overlap, and an instant failure
  cannot become a tight loop.
- Every network request waits at most 10 seconds for an answer — one rule for
  the whole worker.
- The server sends the time left (`expires_in_seconds`), not a time of day —
  clocks on different computers can disagree.
- No automatic retry when the server cannot be reached, after 10 seconds — if
  the server is not running, retrying will not help, and the user is right
  there to run the command again; 10 seconds covers a slow network without
  looking frozen.
- No automatic retry after a start request times out — the server may already
  have created the pairing, and a retry could create a second one.
- The record is a *pairing request* — it is temporary; a successful pairing
  will later create a separate, permanent record for the computer.
- Opening the approval page is a `GET` — reading must never change anything (R6).
- The pairing code is stored as it is, while the keys and the credential are
  hashed — the code is useless without the polling key, which is only stored
  hashed, and it lives at most 10 minutes; storing it lets the page show the
  same code again after a reload (R11).
- Only the server counts tries — a single count cannot disagree with itself.
- Every status change is one conditional `UPDATE`, and the record is read only
  when nothing changed — reading first and writing afterwards lets two
  simultaneous requests both believe they won.
- The worker stops polling once the pairing is accepted — from then on it only
  submits codes.
- Machines live in their own table — a pairing request is temporary and holds
  only the pairing state; a machine is permanent and holds the credential.
- A machine is created at the correct code, as `pending` — the acknowledgment
  needs a stored credential hash to check against.
- Only `active` machines are visible — a credential nobody confirmed must not
  look like a paired computer.
- A pending machine expires after 5 minutes — saving and acknowledging take
  seconds; 5 minutes leaves room for a slow network, and an unconfirmed
  credential does not linger.
- The worker never sends a code again after a lost answer — a correct code's
  credential cannot be issued twice, so resending cannot help; the user pairs
  again.
- The credential is never stored in plain form, even for resending — nothing
  secret waits on the server; a lost credential means pairing again.
- The acknowledgment is safe to repeat, and the worker retries it 3 times,
  2 seconds apart, showing each try — a lost answer must not leave the worker
  unsure whether it is paired, and a repeated acknowledgment cannot create
  anything.
- The worker writes `state.json` before each step it describes
  (`credential_saved: false` before saving, `confirmed` only after the OK) —
  a stop at any moment leaves a note saying what was about to happen, so the
  next run always knows whether to start, finish, recover, or refuse. Without
  it, a lost answer could lead to a second pairing that replaces a working
  credential.
- Ctrl+C stops at once and exits with `130` — the usual code for "stopped by
  the user"; it needs no delay because every stopping point is recoverable.
- An unconfirmed pairing is finished, not restarted — the server may already
  consider the machine active.
- Finishing uses the same `tervi pair --server <url>` command, printed in full
  — no second command for a rare case, and a ready-to-copy line beats "run
  the same command".
- A different `--server` during an unfinished pairing is refused — a credential
  must only ever be sent to the server that issued it.
- The server address is saved only in the secret store entry, next to the
  credential, not in `state.json` — a plain file is easy to edit or copy by
  mistake, and a credential sent to the wrong server cannot be taken back.
- `github.com/zalando/go-keyring` for the secret store — widely used, small,
  and covers Linux, Windows, and macOS. It does not always say whether the
  store is locked or missing, which slice 1's single message does not need.
- Secrets live in their own Go types that print as `[hidden]` — "never in
  logs" is easy to break by accident; a type makes the safe behavior the
  default, and a test checks the full flow's output.
- Every secret store failure is shown to the user — a silent failure would
  look like success, or leave the user guessing.
- The secret store is checked before step 1 with a test value — finding a
  locked or missing store after the user approved and typed the code would
  waste their time and leave a pending machine behind.
- A save is verified by reading it back — it proves the credential can really
  be used later.
- Incomplete local data stops the worker, which changes nothing — repairing it
  belongs to `tervi unpair` in a later slice; until then a hint explains the
  manual clean-up so the user is not stuck.
- Addresses are compared in a standard form — otherwise `http://LOCALHOST:8080/`
  would be refused as a different server.
- Expiry is checked at every request and also cleaned up every minute — the
  check keeps expired credentials useless at every moment; the clean-up only
  keeps the stored status truthful.
- The database's clock decides every expiry — the server and the database run
  in different processes, and two clocks can disagree by a few milliseconds
  exactly at the deadline.
- The approval page polls every 2 seconds, like the worker — simple, and it
  reuses step 3's request; push connections can come with the live chat
  features, where instant updates matter.
- Each failure cause has its own sentence, recorded as `failure_reason` — the
  user needs to know what went wrong to know what to do next.
- Success is shown on the approval page itself — the frontend is the easiest
  part to change, so a separate Machines page can come later.

## Requirement coverage

| Requirement | Tasks |
| --- | --- |
| R1 (usage) | 05 |
| R2 (already paired) | 05 |
| R3 (secret store usable) | 05 |
| R4 (server unreachable) | 06 |
| R5 (show details, link, expiry) | 02, 06 |
| R6 (page shows details; opening changes nothing) | 02, 03 |
| R7 (Pair shows the code) | 02, 03 |
| R8 (worker never shows the code) | 07 |
| R9 (rejection reported within seconds) | 02, 03, 06, 08 |
| R10 (first decision stays) | 02 |
| R11 (reopening shows the current state) | 02, 03 |
| R12 (code asked only after approval; same expiry shown again) | 06, 07 |
| R13 (wrong codes, 5 tries, both sides say it failed) | 03, 04, 07, 08 |
| R14 (a code works only for its pairing) | 04 |
| R15 (10-minute expiry) | 01, 02, 04, 06, 07 |
| R16 (finished pairings accept no code) | 02, 03, 04 |
| R17 (correct code gives the credential) | 04, 07 |
| R18 (credential only in the secret store; never printed) | 01, 05, 07, 08 |
| R19 (success shown in terminal and browser) | 03, 04, 07, 08 |
| R20 (saving fails → not completed) | 07 |
| R21 (localhost only until sign-in) | 01 |
| R22 (only hashes, except the pairing code) | 01, 02, 04 |
| R23 (Ctrl+C) | 06, 07, 08 |
| R24 (retry polling until the deadline) | 06 |
| R25 (saving failure reported) | 04, 07 |
| R26 (no confirmation in 5 minutes → fail) | 04 |
| R27 (not shown as paired until confirmed) | 03, 04 |
| R28 (confirmation retried, then the finish command) | 07, 08 |
| R29 (next run finishes or restarts) | 05, 07 |
| R30 (credential only to its saved server) | 05, 07 |
| R31 (incomplete local data) | 05 |

## Tasks

| # | Task | Depends on | Risk | Status |
| --- | --- | --- | --- | --- |
| 01 | [Server foundation](tasks/01-server-foundation.md) | — | core | todo |
| 02 | [Server: start, poll, and approval API](tasks/02-start-poll-approval.md) | 01 | core | todo |
| 03 | [Approval web page](tasks/03-approval-page.md) | 02 | low | todo |
| 04 | [Server: code, credential, finishing](tasks/04-code-and-credential.md) | 02 | core | todo |
| 05 | [Worker: command and local state](tasks/05-worker-local.md) | 01 | core | todo |
| 06 | [Worker: start and polling](tasks/06-worker-start-and-poll.md) | 02, 05 | core | todo |
| 07 | [Worker: code, saving, and confirmation](tasks/07-worker-code-and-confirm.md) | 04, 06 | core | todo |
| 08 | [End-to-end test](tasks/08-end-to-end.md) | 03, 07 | normal | todo |

```text
01 ──► 02 ──► 03 ───────────────────┐
 │      ├───► 04 ──────┐            │
 │      └───────┐      ▼            ▼
 └──► 05 ─────► 06 ──► 07 ───────► 08
```

06 needs 02 and 05; 07 needs 04 and 06; 08 needs 03 and 07.

Task 05 can be built at the same time as 02–04; tasks 03 and 04 can be built
at the same time, since they change different packages. Each task changes its
own row in this table; when two parallel tasks' rows conflict, the architect
resolves it while rebasing.

## Parked

For a later slice (move to `slices/backlog.md` when this plan is done):

- **Sign-in, so only the server's owner can approve a pairing. Required before
  the server is reachable from any other computer (R21).**
- Custom computer names chosen by the user.
- A Machines page listing every active machine.
- `tervi unpair`: remove a pairing. Then the "isn't finished yet" message also
  offers unpairing, and incomplete local data can be cleaned up by the command
  instead of by hand.
