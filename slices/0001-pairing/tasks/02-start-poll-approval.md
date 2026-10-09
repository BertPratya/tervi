# Task 02 — Server: start, poll, and approval API

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Add the endpoints for the first half of pairing: the worker starts a pairing
and polls its status; the approval page reads the pairing and clicks Accept or
Reject. Two simultaneous clicks must never both win.

## Requirements (copied from the spec)

- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting). *This task provides the link and the time left.*
- R6. When someone opens the approval link, the server shall show the computer
  name and OS, as reported by the worker, with Accept and Reject. Opening the
  link shall not change the pairing.
- R7. When Accept is clicked on a pairing that is waiting for approval, the server
  shall show a pairing code in the browser.
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R10. Once Accept or Reject has been clicked, later clicks on either button,
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

## Context

### What exists (task 01)

- Tables `pairing_requests` (`id`, `hostname`, `os_name`, `os_version` — `''`
  means unknown — `status`, `polling_key_hash`, `approval_key_hash`,
  `pairing_code`, `tries_left` default 5, `failure_reason`, `created_at`,
  `expires_at`) and `machines` (`machine_id`, `pairing_request_id`,
  `hostname`, `os_name`, `os_version`, `display_name`, `credential_hash`,
  `status`, `created_at`, `expires_at`).
- `internal/secret`: `secret.Value` (prints `[hidden]`; read with
  `Reveal()`), `NewKey()`, `NewPairingCode()` (`NNNN-NNNN`), `FromString(s)`,
  `Hash(v)` (SHA-256 bytes).
- `internal/pairing`: an empty `Register(mux, Deps{Pool, PublicURL, Logger})`
  that `internal/server.New` already calls. `PublicURL` has no trailing `/`.

### Proofs and errors

Proofs travel in `Authorization: Bearer <proof>`. A missing or malformed
header is treated like an unknown proof. Every error answer is
`{"error": "<code>"}`. Request bodies over 4 KB → `400 {"error": "invalid_input"}`.

### Expiry applied when reading

With the **database's** clock (`now()` in SQL): a pairing request whose stored
status is `waiting_for_approval` or `waiting_for_code` and whose `expires_at`
is past reads as `expired`. Do not change the stored status here; a clean-up
job does that in task 04. Every other status never expires.

### Worker endpoint 1 — `POST /api/v1/pairings` (no proof)

Request (each field optional; unknown fields ignored):
`{"hostname": "bert-desktop", "os_name": "Ubuntu", "os_version": "26.04"}`.
Limits counted in characters (runes): `hostname` ≤ 64, `os_name` ≤ 64,
`os_version` ≤ 32. Over a limit, or a value containing a NUL character
(`\u0000`, which PostgreSQL cannot store in `text`) → `400 {"error": "invalid_input"}`,
nothing stored. A missing field is stored as `''`.

1. Make a polling key and an approval key with `secret.NewKey()`.
2. Insert a row: `status = 'waiting_for_approval'`, both hashes,
   `expires_at = now() + interval '10 minutes'`.
3. Answer `201`:
   `{"polling_key": "<43 chars>", "approval_url": "<PublicURL>/pair/<approval key>", "expires_in_seconds": 600}`.

The link's address comes only from `PublicURL`, never from the request's
`Host` header.

### Worker endpoint 2 — `GET /api/v1/pairings/current` (proof: polling key)

Find the row with `polling_key_hash = Hash(key)`. Unknown →
`401 {"error": "unknown_key"}`. Answer `200`:
`{"status": "waiting_for_approval", "expires_in_seconds": 412}`, plus
`"tries_left"` (the stored value) when the status is `waiting_for_code`.
`expires_in_seconds` is the whole seconds left, rounded up, never below 0.

### Approval endpoints (proof: approval key)

Find the row with `approval_key_hash = Hash(key)`. Unknown, missing, or
malformed → `404 {"error": "invalid_link"}` (the same answer in every case).

| Method and path | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | Reads. Changes nothing. |
| `POST /api/v1/approvals/current/accept` | The **Accept** button: `waiting_for_approval` → `waiting_for_code`, storing a new pairing code |
| `POST /api/v1/approvals/current/reject` | The **Reject** button: `waiting_for_approval` → `rejected` |

All three answer `200` with:

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

- `pairing_code` only while `waiting_for_code` — always the same stored code.
- `failure_reason` only when `failed`.
- `display_name` only when `paired`, from the `machines` row whose
  `pairing_request_id` is this pairing (it exists from task 04 on).
- `already_decided: true` only on accept or reject when another request
  changed the status first. An **expired** request answers `expired` with
  `already_decided: false`, since no click decided it.

### Accept and reject without a race

Change first, in one statement; read only if nothing changed:

```sql
UPDATE pairing_requests
   SET status = 'waiting_for_code', pairing_code = $2
 WHERE approval_key_hash = $1
   AND status = 'waiting_for_approval'
   AND expires_at > now()
RETURNING ...;
```

(Reject is the same with `status = 'rejected'` and no code.)

1. A row came back → this request made the change → answer the new state,
   `already_decided: false`.
2. Nothing came back → read the row. Not found → `404 invalid_link`. Expired
   → `expired`, `already_decided: false`. Otherwise → the real state,
   `already_decided: true`.

Never read first and update afterwards: two simultaneous requests could both
read `waiting_for_approval` and both believe they won.

### Where the code goes

`internal/pairing/`: the handlers, their queries, and the five routes, added
inside `Register`.

### Secrets in code

Keys and the pairing code stay in `secret.Value` until written into a response
or hashed. No log line, error message, or test failure message may contain
their values.

### Tests and the database

Every database test gets its own database from `dbtest.New(t)` (task 01,
package `internal/db/dbtest`), so tests never share tables. Start PostgreSQL
with `docker compose up -d`. If the database cannot be reached from your
environment, stop and report it; do not skip those tests.

## Boundaries

- May create or change: `internal/pairing/`.
- Must not change: everything else, except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestStartReturnsKeyLinkAndTimeLeft` — `201`, a 43-character polling key, a link `<PublicURL>/pair/<43 chars>`, `expires_in_seconds` = 600 | R5 |
| `TestStartStoresOnlyHashes` — neither raw key appears in any column; the stored hashes equal `Hash` of the returned values | R22 |
| `TestStartKeysAreUnique` — two starts never return the same polling key or approval key | R22 |
| `TestStartIgnoresHostHeader` — a forged `Host` header does not change the link | Links use only the configured address |
| `TestStartLimits` — values at exactly 64/64/32 characters, including Thai, are accepted; one more character gives `400` and stores nothing; a value containing `\u0000` gives `400` and stores nothing; a 5 KB body gives `400` | Input limits |
| `TestStartMissingFields` — `{}` is accepted and stored as `''` | Unknown details allowed |
| `TestPollWaiting` — a fresh pairing polls as `waiting_for_approval`, `expires_in_seconds` between 595 and 600 | R9 |
| `TestPollUnknownKey` — unknown or missing key: `401 unknown_key` | Proof required |
| `TestPollAfterExpiry` — `expires_at` set to the past by SQL: the poll answers `expired`, the stored status is still `waiting_for_approval` | R15 |
| `TestReadShowsDetailsAndChangesNothing` — approval `GET` returns the details and `waiting_for_approval`; the row is unchanged | R6 |
| `TestAcceptCreatesCode` — accept gives `waiting_for_code` and a code matching `^\d{4}-\d{4}$`; a poll now includes `tries_left: 5` | R7 |
| `TestReopenShowsSameCode` — `GET` after accept returns the same code | R11 |
| `TestReject` — reject gives `rejected`; a poll answers `rejected` | R9 |
| `TestSecondClickDoesNotChangeDecision` — accept then reject, and reject then accept: the first decision stays; the second answer has `already_decided: true` and the real state | R10 |
| `TestSimultaneousClicks` — 50 rounds of accept and reject sent at the same moment: in each round exactly one has `already_decided: false`, and the stored status matches it | R10 |
| `TestExpiredCannotBeDecided` — `expires_at` in the past: accept and reject change nothing and answer `expired`, `already_decided: false`, no code | R15, R16 |
| `TestInvalidLink` — unknown, missing, or malformed approval key: `404 invalid_link` | Reveals nothing about other pairings |
| `TestFailedShowsReason` — status `failed` with a `failure_reason`, set by SQL: `GET` returns both and no code | R16 |
| `TestOldRejectedNeverExpires` — a `rejected` row past `expires_at` still reads `rejected` | Only waiting statuses expire |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The approval page (task 03), the code, the credential, machines, and the
clean-up job (04), the worker (05, 06, 07).

## If anything is unclear

Stop and report the question. Do not guess.
