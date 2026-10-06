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

### Names

| Name | What it is |
| --- | --- |
| Pairing request | The record of one pairing attempt |
| Approval key | The random key in the approval link `/pair/<approval key>` |
| Polling key | The worker's own key; it polls and submits the code with it |
| Pairing code | The code the browser shows and the user types into the terminal |
| Credential | The permanent proof the worker receives at the end |

## Flow

### Step 1 — The worker starts a pairing

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server |
| Endpoint | `POST /api/v1/pairings` |
| Sends | `hostname` (≤ 64), `os_name` (≤ 64), `os_version` (≤ 32). Each one is optional. |
| Server does | Creates a pairing request with status `waiting_for_approval` and `expires_at` 10 minutes later. Generates a polling key and an approval key and stores only their hashes. |
| Server answers | The polling key, the approval link (`/pair/<approval key>`), and `expires_at`, which the terminal shows as the expiry time (R5) |
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
| Worker then | Saves the credential in the OS secret store (R18), then step 7a or 7b |

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
| Worker shows | `✓ Paired successfully.` |
| Browser shows | The approval page changes to the result, with the display name, OS name, and OS version: `✓ Paired: bert-desktop · Ubuntu 26.04` (R19) |

## Technical decisions

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
- Success is shown on the approval page itself — the frontend is the easiest
  part to change, so a separate Machines page can come later.

## Parked

To add later in this plan:

- Local check before step 1: already paired (R2).
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
