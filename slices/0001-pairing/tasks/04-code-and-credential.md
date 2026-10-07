# Task 04 — Server: code, credential, finishing

Slice: 0001-pairing    Risk: core    Depends on: 02

## Goal

The server side of the end of pairing: checking the code the user typed,
issuing the credential and a pending machine, accepting the worker's
acknowledgment or its "saving failed" report, and the expiry clean-up job.
The server must never issue two credentials for one pairing.

## Requirements (copied from the spec)

- R13. If a wrong code is typed, then the worker shall show how many tries are
  left. After 5 wrong codes, the pairing shall fail, and both the terminal and
  the browser shall say so.
- R14. A pairing code shall only work for the pairing that created it.
- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R16. While a pairing is expired, rejected, or failed, the link shall show only
  the result, and no code shall be accepted.
- R17. When the correct code is typed, the worker shall receive its credential.
- R19. When the credential is saved, the worker shall confirm with the server.
  Then the terminal shall show `✓ Paired successfully.`, and the browser shall
  show that the computer is paired, with its name and OS, and that the tab can
  be closed.
- R22. The server shall store only hashes of the approval key, the polling key,
  and the credential. The pairing code may be stored as it is, because it is
  useless without the polling key and lives at most 10 minutes.
- R25. If saving the credential fails, then the worker shall report it, and the
  pairing shall fail; the browser shall show that it failed.
- R26. If the worker has not confirmed within 5 minutes after the correct code,
  then the pairing shall fail, and its credential shall never work.
- R27. Until the worker confirms, the computer shall not appear anywhere as
  paired.

## Context

### What exists (tasks 01, 02)

- Tables `pairing_requests` (`id`, `hostname`, `os_name`, `os_version`,
  `status`, `polling_key_hash`, `approval_key_hash`, `pairing_code`,
  `tries_left` default 5, `failure_reason`, `created_at`, `expires_at`) and
  `machines` (`machine_id`, `pairing_request_id` unique, `hostname`,
  `os_name`, `os_version`, `display_name`, `credential_hash` unique, `status`
  one of `pending`/`active`/`failed`/`expired`, `created_at`, `expires_at`).
- `internal/secret`: `secret.Value` (prints `[hidden]`; `Reveal()`),
  `NewKey()`, `FromString(s)`, `Hash(v)`, `NormalizeCode(s)`.
- `internal/pairing`: start, poll, and the approval endpoints, registered in
  `Register`; an empty `StartCleanup(ctx, pool, logger)` that
  `internal/server.Run` already calls. Reads apply expiry: a request in
  `waiting_for_approval` or `waiting_for_code` past `expires_at` reads as
  `expired`. The approval read answers `display_name` when `paired`.

### Proofs and errors

Proofs travel in `Authorization: Bearer <proof>`. Every error answer is
`{"error": "<code>"}`. Bodies over 4 KB → `400 {"error": "invalid_input"}`.

### Endpoint 3 — `POST /api/v1/pairings/current/code` (proof: polling key)

Request: `{"code": "4827-1934"}`. Unknown polling key →
`401 {"error": "unknown_key"}`. Otherwise `200` with one `result`:

| `result` | Other fields | When |
| --- | --- | --- |
| `accepted` | `credential`, `machine_id` | The code matches |
| `wrong_code` | `tries_left` | It does not match, and tries remain |
| `failed` | — | It does not match, and no tries remain |
| `expired` | — | The status after expiry is applied is `expired`: stored `expired` (for example after the clean-up job ran), or `waiting_for_approval` / `waiting_for_code` past `expires_at` |
| `not_waiting_for_code` | `status` | Any other status, including `finishing` after an earlier correct code, and `rejected` or `failed` however old. `status` is the status after expiry is applied, the same value the poll would answer (for example `failed` for a `finishing` request whose machine has expired). |

"The status after expiry is applied" is the same value the poll answers. So
whether or not the clean-up job has run yet, a late code always gets the same
answer.

Compare `NormalizeCode(submitted)` with `NormalizeCode(stored)` in constant
time, only against the row found by this polling key (R14).

**Correct code**, in one transaction:

1. ```sql
   UPDATE pairing_requests SET status = 'finishing'
    WHERE id = $1 AND status = 'waiting_for_code' AND expires_at > now()
   RETURNING ...;
   ```
   If no row came back, create nothing: read the row and answer `expired` or
   `not_waiting_for_code`. **This condition makes a second credential
   impossible.**
2. Make the credential with `secret.NewKey()`.
3. Insert the machine: `status = 'pending'`,
   `expires_at = now() + interval '5 minutes'`,
   `credential_hash = Hash(credential)`, `display_name` = the hostname (or
   `unknown` if empty), hostname and OS copied from the pairing request.
4. Commit, then answer `accepted` with the credential and `machine_id`. The
   credential itself is never stored.

**Wrong code**, in one statement:

```sql
UPDATE pairing_requests
   SET tries_left = tries_left - 1,
       status = CASE WHEN tries_left - 1 = 0 THEN 'failed' ELSE status END,
       failure_reason = CASE WHEN tries_left - 1 = 0 THEN 'wrong_codes' ELSE failure_reason END
 WHERE id = $1 AND status = 'waiting_for_code' AND tries_left > 0 AND expires_at > now()
RETURNING tries_left, status;
```

A row came back → `wrong_code` with the new `tries_left`, or `failed` at 0.
No row → read the row and answer with its real state.

### Endpoints 4 and 5 (proof: credential)

Find the machine with `credential_hash = Hash(credential)`. Unknown →
`401 {"error": "unknown_credential"}`. No body. Answer `200` with one `result`:

| Machine | 4 — `POST /api/v1/machines/current/acknowledgment` | 5 — `POST /api/v1/machines/current/save-failure` |
| --- | --- | --- |
| `pending`, not past `expires_at` | `ok`: machine → `active`, its pairing request → `paired` (one transaction) | `ok`: machine → `failed`, its pairing request → `failed` with `not_saved` (one transaction) |
| `active` | `ok`, nothing changes (safe to repeat) | `active`, nothing changes |
| `failed` | `failed` | `failed` |
| `expired`, or `pending` past `expires_at` | `expired` | `expired` |

Each change is a conditional `UPDATE ... WHERE credential_hash = $1 AND status = 'pending' AND expires_at > now() RETURNING ...`;
when no row comes back, read the machine and answer from the table above.

### Expiry applied when reading (extend task 02's reads)

With the database's clock: a pairing request in `finishing` whose machine is
`pending` and past its `expires_at` reads as `failed` with `not_confirmed`,
for polling and for the approval read. No read anywhere answers `paired` while
the machine is `pending` (R27).

### The clean-up job — fill in `StartCleanup`

Runs every minute until `ctx` is cancelled. Each pass, with the database's
clock and conditional updates:

- pairing requests in `waiting_for_approval` or `waiting_for_code` past
  `expires_at` → `expired`;
- machines `pending` past `expires_at` → `expired`, and their pairing requests
  in `finishing` → `failed` with `not_confirmed` (same transaction).

Make the interval a parameter of an internal function so tests can run one
pass directly. The job only makes stored statuses truthful; the checks above
already refuse expired records.

### Where the code goes

`internal/pairing/`: the three endpoints (added inside `Register`), the read
extension, and `StartCleanup`.

### Secrets in code

The polling key, pairing code, and credential stay in `secret.Value` until
written into a response, compared, or hashed. No log line, error message, or
test failure message may contain their values.

### Tests and the database

Every database test gets its own database from `dbtest.New(t)` (task 01,
package `internal/db/dbtest`), so tests never share tables, and one clean-up
pass cannot touch another test's rows. Start PostgreSQL with
`docker compose up -d`. If the database cannot be reached from your
environment, stop and report it; do not skip those tests.

## Boundaries

- May create or change: `internal/pairing/`.
- Must not change: everything else, except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestCorrectCodeIssuesCredential` — `accepted` with a 43-character credential and a `machine_id`; request `finishing`; machine `pending`, `expires_at` about 5 minutes ahead | R17 |
| `TestCodeFormatsAccepted` — `48271934` and `4827 1934` both match `4827-1934` | Easy typing |
| `TestCredentialOnlyHashed` — the raw credential is in no column of either table; `credential_hash` equals `Hash` of the returned credential | R22 |
| `TestNoSecondCredential` — a second correct code answers `not_waiting_for_code` with `finishing`; one machine only | Never two credentials |
| `TestSimultaneousCorrectCodes` — 20 rounds of two correct codes at once: exactly one `accepted` and one machine per round | Never two credentials |
| `TestWrongCodeCountsDown` — wrong codes give `tries_left` 4, 3, 2, 1, then `failed`; the request is `failed` / `wrong_codes`; the approval read shows it | R13 |
| `TestSimultaneousWrongCodes` — 10 wrong codes at once: exactly 5 answers are `wrong_code` or `failed`, the other 5 `not_waiting_for_code`; final `tries_left` 0, status `failed` / `wrong_codes` | R13 |
| `TestCodeOnlyForItsPairing` — pairing A's code with pairing B's polling key counts as a wrong code for B | R14 |
| `TestNoCodeWhenFinished` — for `expired`, `rejected`, and `failed` requests, even the right code answers `expired` or `not_waiting_for_code` and creates no machine | R15, R16 |
| `TestOldRejectedAnswersNotWaiting` — a `rejected` request past `expires_at` answers `not_waiting_for_code` with `rejected` | Only waiting statuses expire |
| `TestAcknowledgment` — machine `active`, request `paired`; the approval read shows `paired` with `display_name` | R19, R27 |
| `TestAcknowledgmentRepeatable` — a second acknowledgment answers `ok` and changes nothing | Safe to retry |
| `TestAcknowledgmentAfterExpiry` — machine `expires_at` in the past: `expired`; the machine never becomes `active`; the request reads `failed` / `not_confirmed` | R26 |
| `TestSaveFailure` — machine `failed`, request `failed` / `not_saved`; a later acknowledgment answers `failed` | R25 |
| `TestSaveFailureOnActive` — on an `active` machine: `active`, nothing changes | Defined answer |
| `TestNotPairedUntilConfirmed` — while the machine is `pending`, neither the poll nor the approval read answers `paired` | R27 |
| `TestCleanupPass` — one pass expires an old waiting request, expires an old pending machine and fails its request with `not_confirmed`, and leaves fresh records alone | R15, R26 |
| `TestUnknownCredential` — unknown credential on 4 and 5: `401 unknown_credential` | Proof required |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The approval page (task 03), the worker (05, 06, 07), the end-to-end test (08).

## If anything is unclear

Stop and report the question. Do not guess.
