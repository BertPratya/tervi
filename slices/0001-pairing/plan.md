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
| `pairing_code` | Created when the user accepts. How it is stored is parked (R11 vs R22). |
| `tries_left` | Wrong codes still allowed. Starts at 5. |
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
service `tervi`, user `worker`. Its value holds the credential together with
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
| `confirmed` | `false` from just before the acknowledgment is sent; `true` once the server answered OK |

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

**A valid server address** starts with `http://` or `https://` and has a host,
with an optional port: `http://localhost:8080`. An invalid address stops the
command before anything is contacted.

## Flow

### Step 0 — The worker reads its state

Before contacting the server, the worker reads `state.json` and its secret
store entry. "Saved server" below always means the server in the secret store
entry, compared with `--server` in standard form (see Mechanisms).

| `state.json` | Worker does |
| --- | --- |
| Missing | Starts a new pairing (step 1) |
| `confirmed: true` | Already paired (R2) |
| `confirmed: false`, `--server` equals the saved server | Finishes the earlier pairing: sends the acknowledgment again, as in step 7b, instead of starting a new pairing |
| `confirmed: false`, `--server` differs from the saved server | Refuses and changes nothing: `✗ A pairing with <saved server> isn't finished yet.` followed by `To finish it, run:  tervi pair --server <saved server>` |

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
| Server answers | The polling key, the approval link (`<public address>/pair/<approval key>`), and `expires_at`, which the terminal shows as the expiry time (R5) |

**The approval link's address** comes from the server's own setting,
`TERVI_PUBLIC_URL`: the address other devices use to reach the server. It is
never taken from the request. The worker shows the link exactly as received.
In this slice the public address is `http://localhost:8080`.
| If the request times out after it was sent | The worker does not retry. It says the pairing could not be started and the user can run the command again. A pairing the server did create expires by itself. |

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
| Worker then | `waiting_for_approval`: poll again. `rejected` or `expired`: say so and stop. `waiting_for_code`: stop polling and ask for the code (step 5); the answer includes `tries_left`. |

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

### Step 5 — The worker submits the pairing code

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server |
| Sends | The pairing code the user typed, and the polling key |
| Code matches | Step 6 |
| Code does not match | `tries_left` goes down by 1. The server answers with the new `tries_left`, and the worker shows it. At 0 the status becomes `failed` and the worker stops. |

**Only the server counts tries.** The worker shows the number the server sends
and never counts by itself, so the two can never disagree, for example after
a lost response or a restarted terminal.

### Step 6 — The server issues the credential

| Question | Answer |
| --- | --- |
| When | Step 5's code matches |
| Server does | Creates a credential and a machine with status `pending` and `expires_at` 5 minutes later, storing only the credential's hash. The pairing request's status becomes `finishing`. |
| Server answers | The credential |
| Worker then | Saves the credential together with the server address in its secret store entry (R18), then step 7a or 7b |

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
| Worker first | Writes `state.json` with `confirmed: false` |
| Worker sends | The acknowledgment, with the credential as proof |
| Server does | The machine's status becomes `active`; the pairing request's status becomes `paired`. The server answers OK. |
| Worker then | Updates `state.json` to `confirmed: true` and shows `✓ Paired successfully.` |

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
| Browser shows | The approval page changes to the result, with the display name, OS name, and OS version: `✓ Paired: bert-desktop · Ubuntu 26.04` (R19) |

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
- Exit codes `0` paired, `1` failure, `2` wrong usage — scripts can tell the
  three apart; finer codes can come later.
- The approval link uses the server's configured public address, never the
  worker's `--server` value or the request's `Host` header — the worker and a
  phone often need different addresses for the same server, and a request's
  `Host` header can be forged to point links at another site.
- Resource-style paths (`POST /api/v1/pairings` creates a pairing) — the common
  REST convention; every endpoint in this plan follows the same style.
- The hostname is the name shown to the user — custom names come later.
- Hostname and OS name up to 64 characters, OS version up to 32 — 64 is the
  longest hostname Linux allows, and real OS names and versions are far shorter.
- No automatic retry after a start request times out — the server may already
  have created the pairing, and a retry could create a second one.
- The record is a *pairing request* — it is temporary; a successful pairing
  will later create a separate, permanent record for the computer.
- Opening the approval page is a `GET` — reading must never change anything (R6).
- Only the server counts tries — a single count cannot disagree with itself.
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
- The worker writes `state.json` with `confirmed: false` before acknowledging,
  and `true` after the OK — whatever goes wrong in between, the next run knows
  whether to start, finish, or refuse. Without it, a lost answer could lead to
  a second pairing that replaces a working credential.
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
- Addresses are compared in a standard form — otherwise `http://LOCALHOST:8080/`
  would be refused as a different server.
- Expiry is checked at every request and also cleaned up every minute — the
  check keeps expired credentials useless at every moment; the clean-up only
  keeps the stored status truthful.
- The database's clock decides every expiry — the server and the database run
  in different processes, and two clocks can disagree by a few milliseconds
  exactly at the deadline.
- Success is shown on the approval page itself — the frontend is the easiest
  part to change, so a separate Machines page can come later.

## Parked

To add later in this plan:

- Already paired (R2): the exact message, and what to do when `state.json` and
  the credential disagree (one exists without the other).
- Local check before step 1: the OS secret store is usable (R3).
- Rename "worker's own secret" to "polling key" in `spec.md`.
- Update `spec.md` for the machine expiry and the "saving failed" report
  (it currently leaves this cleanup to slice 2).
- If step 5's answer is lost after the code matched, may the worker send the
  code again? (The server must never issue a second credential.)
- R21: anyone with the approval link can approve.
- R11 vs R22: showing the pairing code again.

For a later slice (move to `slices/backlog.md` when this plan is done):

- Custom computer names chosen by the user.
- A Machines page listing every active machine.
- `tervi unpair`: remove a pairing. Then the "isn't finished yet" message also
  offers unpairing.
