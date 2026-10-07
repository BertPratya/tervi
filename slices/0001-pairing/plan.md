# 0001 Pairing — plan

Status: draft
Spec: [spec.md](spec.md)

## Data

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
waiting_for_approval ──Accept──► waiting_for_code ──correct code──► finishing ──acknowledged──► paired
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
| Unknown command, or no command | `Unknown command: <command>` + usage | `2` |
| Ctrl+C at any point | See "Cancelling with Ctrl+C" | `130` |

**A valid server address** starts with `http://` or `https://` and has a host,
with an optional port: `http://localhost:8080`. An invalid address stops the
command before anything is contacted.

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
| `credential_saved: false` | Exists | The save happened but was not recorded. Reads the entry back to verify it, sets `credential_saved: true`, then continues as in the next row. |
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

**A credential only ever goes to the server that issued it.** That is why a
different `--server` is refused instead of used. Once `tervi unpair` exists
(a later slice), this message will also offer unpairing.

**When finishing an earlier pairing**, the server's answer decides:

- OK → `confirmed: true`, and `✓ Paired successfully.`
- The machine expired or failed → the credential will never work, so the
  worker deletes `state.json` and its secret store entry, and
  shows: `✗ The earlier pairing didn't finish in time. Run the same command again to start a new one.`

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
| Worker then | `waiting_for_approval`: poll again. `rejected` or `expired`: say so and stop. `waiting_for_code`: stop polling and ask for the code (step 5); the answer includes `tries_left` and `expires_in_seconds`, and the worker resets its deadline from it. |
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
| First failed poll | `Connection lost, retrying...` (once, not on every failure) | Keep polling |
| First successful poll after failures | `Connection restored.` | Keep polling |
| `rejected` | `✗ Pairing was rejected in the browser.` | Stop, exit code `1` |
| `expired`, or the worker's deadline passes while the server cannot be reached | `✗ The link expired. Run the command again.` | Stop, exit code `1` |
| `waiting_for_code` | `✓ Approved in the browser.` then `Type the code shown in the browser (expires at 14:32, 5 tries left):` | Step 5 |

### Step 3 — The user opens the approval link

| Question | Answer |
| --- | --- |
| Who → who | Browser → Server |
| Request | `GET`, with the approval key. Opening the page changes nothing (R6). |
| Server answers | `hostname`, `os_name`, `os_version`, and the status |
| Browser then | Shows the computer's details with **Accept** and **Reject** |

Sign-in before this step comes in a later slice.

### Step 4 — The user accepts or rejects

| Question | Answer |
| --- | --- |
| Who → who | Browser → Server, with the approval key |
| Reject | Status becomes `rejected`. The worker learns it at its next poll. |
| Accept | Status becomes `waiting_for_code`. The server creates the pairing code, saves it, and the page shows it and asks the user to type it into the terminal. |

### The approval page stays up to date

The page cannot learn about changes by itself, so while it shows the buttons
or the pairing code, it **polls**: it repeats step 3's `GET` 2 seconds after
the previous poll ended. It stops polling once it shows a result. Covers R13,
R19.

| Status | The page shows |
| --- | --- |
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
| Worker sends | "Saving failed", with the credential as proof |
| Server does | The machine's status becomes `failed`; the pairing request's status becomes `failed`, and the browser shows that pairing failed |

This report may never arrive, for example when the network is down or the
worker crashed. The machine's 5-minute expiry covers that case.

### Step 7b — Saving succeeded

| Question | Answer |
| --- | --- |
| Worker sends | The acknowledgment, with the credential as proof |
| Server does | The machine's status becomes `active`; the pairing request's status becomes `paired`. The server answers OK. |
| Worker then | Updates `state.json` to `confirmed: true` and shows `✓ Paired successfully.` |
| Browser shows | The approval page changes to the result, with the display name, OS name, and OS version: `✓ Paired: bert-desktop · Ubuntu 26.04` (R19) |

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
| From step 6's first write onward | `Pairing cancelled before it was confirmed.` followed by `To finish, run:  tervi pair --server <saved server>` | `130` |

Ctrl+C never needs to wait: whatever was written, the next run knows what to
do (step 0).

## Mechanisms

Methods that apply to several steps.

### Expiry

Applies to: pairing requests (10 minutes) and pending machines (5 minutes).
Covers: R15, R16, R26.

Two separate jobs:

1. **Enforcing (at every request).** Before it reads or changes a pairing
   request or a machine, the server compares `expires_at` with the current
   time. Anything past its `expires_at` is treated as expired, even if its
   stored status still says otherwise: a read answers `expired`, and a change
   is refused. This alone keeps R26 true at every moment.
2. **Cleaning up (every minute).** A background job updates the stored status
   of everything past its `expires_at`:
   - pairing requests in `waiting_for_approval` or `waiting_for_code` become `expired`;
   - pending machines become `expired`, and their pairing requests, in
     `finishing`, become `failed`.

The clean-up only makes the stored status truthful. If it runs late or stops,
nothing unsafe happens, because step 1 already refuses expired records.

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
| 4 | Accept → `waiting_for_code`; Reject → `rejected` | status is `waiting_for_approval` |
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

### Secrets never printed

Applies to: the approval key, the polling key, the pairing code, and the
credential, in both the worker and the server. Covers: R18, R22.

Each secret is held in its own small Go type, whose printed form is always
`[hidden]`, never its value. So printing a value, logging an error, or
dumping a whole answer can never reveal a secret by accident. The value is
read only where it is really needed: sending it, hashing it, or saving it in
the secret store.

A test runs the full pairing flow, captures everything printed and logged by
the worker and the server, and fails if any secret's value appears in it.

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

## Parked

To add later in this plan:

- Rename "worker's own secret" to "polling key" in `spec.md`.
- Update `spec.md` for the machine expiry and the "saving failed" report
  (it currently leaves this cleanup to slice 2).
- If step 5's answer is lost after the code matched, may the worker send the
  code again? (The server already cannot issue a second credential; see Status
  changes. What remains is what the worker does and shows.)
- R21: anyone with the approval link can approve.

For a later slice (move to `slices/backlog.md` when this plan is done):

- Custom computer names chosen by the user.
- A Machines page listing every active machine.
- `tervi unpair`: remove a pairing. Then the "isn't finished yet" message also
  offers unpairing, and incomplete local data can be cleaned up by the command
  instead of by hand.
