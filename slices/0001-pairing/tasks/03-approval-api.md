# Task 03 — Server: approval API

Slice: 0001-pairing    Risk: core    Depends on: 02

## Goal

Add the three endpoints the approval page uses: reading a pairing's details,
accepting it, and rejecting it. Two simultaneous clicks must never both win.

## Requirements (copied from the spec)

- R6. When someone opens the approval link, the server shall show the computer
  name and OS, as reported by the worker, with Pair and Reject. Opening the
  link shall not change the pairing.
- R7. When Pair is clicked on a pairing that is waiting for approval, the server
  shall show a pairing code in the browser.
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R10. Once Pair or Reject has been clicked, later clicks on either button,
  from any tab or device, shall not change the decision. The page shall show
  the current state instead.
- R11. When the link is opened again, the page shall show the current state:
  the buttons while waiting for approval, the same code while waiting for the
  code, and otherwise the result (paired, rejected, failed, or expired).
- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R16. While a pairing is expired, rejected, or failed, the link shall show only
  the result, and no code shall be accepted.
- R22. The server shall store only hashes of the approval key, the polling key,
  and the credential. The pairing code may be stored as it is, because it is
  useless without the polling key and lives at most 10 minutes.

In this plan, the spec's "Pair" button is the **accept** action.

## Context

### What exists (tasks 01 and 02)

- Table `pairing_requests` (`id`, `hostname`, `os_name`, `os_version`,
  `status`, `polling_key_hash`, `approval_key_hash`, `pairing_code`,
  `tries_left`, `failure_reason`, `created_at`, `expires_at`) and table
  `machines` (`machine_id`, `pairing_request_id`, `hostname`, `os_name`,
  `os_version`, `display_name`, `credential_hash`, `status`, `created_at`,
  `expires_at`).
- `internal/secret`: `secret.Value`, `NewPairingCode()` (format `NNNN-NNNN`),
  `FromString(s)`, `Hash(v)`.
- `internal/pairing`: start (`POST /api/v1/pairings`) and poll
  (`GET /api/v1/pairings/current`), which applies expiry when reading.

### Proof

The approval key, in `Authorization: Bearer <approval key>`. A row is found by
`approval_key_hash = Hash(key)`. Unknown, missing, or malformed key →
`404 {"error": "invalid_link"}` (the same answer in every case, so it reveals
nothing about other pairings).

### The three endpoints

| Method and path | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | Reads. Changes nothing. |
| `POST /api/v1/approvals/current/accept` | `waiting_for_approval` → `waiting_for_code`, and stores a new pairing code |
| `POST /api/v1/approvals/current/reject` | `waiting_for_approval` → `rejected` |

All three answer `200` with the same body:

```json
{
  "status": "waiting_for_code",
  "hostname": "bert-desktop",
  "os_name": "Ubuntu",
  "os_version": "26.04",
  "already_decided": false,
  "pairing_code": "4827-1934"
}
```

- `pairing_code` only while the status is `waiting_for_code` (the same stored
  code every time, so reopening shows the same code).
- `failure_reason` only when the status is `failed`.
- `display_name` only when the status is `paired` (from the `machines` row
  whose `pairing_request_id` is this pairing; it will exist from task 05 on).
- `already_decided` is `true` only on accept or reject when another request
  changed the status first.

### How accept and reject change the status (no race)

**Change first, in one statement; read only if nothing changed.**

```sql
UPDATE pairing_requests
   SET status = 'waiting_for_code', pairing_code = $2
 WHERE approval_key_hash = $1
   AND status = 'waiting_for_approval'
   AND expires_at > now()
RETURNING ...;
```

(Reject is the same with `status = 'rejected'` and no code.)

1. A row came back → this request made the change → answer with the new state,
   `already_decided: false`.
2. Nothing came back → read the row. Not found → `404 invalid_link`. Found →
   answer with its real (expiry-applied) state and `already_decided: true`.

Never read first and update afterwards: two simultaneous requests could both
read `waiting_for_approval` and both believe they won.

### Expiry applied when reading

Use the database's clock. If `expires_at <= now()` and the stored status is
`waiting_for_approval` or `waiting_for_code`, answer `expired` (and no
`pairing_code`). Do not change the stored status here; the clean-up job in
task 05 does that.

### Where the code goes

- `internal/pairing/`: the handlers and queries (alongside task 02's files).
- `cmd/server/main.go`: register the three routes. Add only your own lines.

### Secrets in code

The approval key and the pairing code stay in `secret.Value` until written
into a response or hashed. No log line, error message, or test failure message
may contain their values.

### Tests and the database

Tests that need PostgreSQL read `DATABASE_URL` (start it with
`docker compose up -d`). If the database cannot be reached from your
environment, stop and report it; do not skip those tests.

## Boundaries

- May create or change: `internal/pairing/`, `cmd/server/main.go`.
- Must not change: `internal/db/migrations/`, `internal/secret/`, task 02's
  endpoints' behavior, `slices/` except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestReadShowsDetailsAndChangesNothing` — `GET` returns hostname, OS name, OS version, and `waiting_for_approval`; the stored row is unchanged afterwards | R6 |
| `TestAcceptCreatesCode` — accept gives `waiting_for_code` and a code matching `^\d{4}-\d{4}$` | R7 |
| `TestReopenShowsSameCode` — `GET` after accept returns the same code | R11 |
| `TestReject` — reject gives `rejected`; a poll with the polling key (task 02) now answers `rejected` | R9 |
| `TestSecondClickDoesNotChangeDecision` — accept then reject, and reject then accept: the first decision stays, the second answer has `already_decided: true` and the real state | R10 |
| `TestSimultaneousClicks` — 50 rounds of accept and reject sent at the same moment from two goroutines: in each round exactly one has `already_decided: false`, and the stored status matches it | R10 |
| `TestExpiredCannotBeDecided` — with `expires_at` in the past, accept and reject change nothing and answer `expired` without a code | R15, R16 |
| `TestInvalidLink` — an unknown, missing, or malformed key gives `404 invalid_link` | — |
| `TestFailedShowsReason` — with the status set to `failed` and a `failure_reason` by SQL, `GET` returns both and no code | R16 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The approval page's HTML and JavaScript (task 04), submitting the code, the
credential, machines, and the expiry clean-up job (task 05), the worker (06, 07).

## If anything is unclear

Stop and report the question. Do not guess.
