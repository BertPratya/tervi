# Task 05 — Server: code, credential, finishing

Slice: 0001-pairing    Risk: core    Depends on: 03

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

### What exists (tasks 01–03)

- Tables `pairing_requests` (`id`, `hostname`, `os_name`, `os_version`,
  `status`, `polling_key_hash`, `approval_key_hash`, `pairing_code`,
  `tries_left` default 5, `failure_reason`, `created_at`, `expires_at`) and
  `machines` (`machine_id`, `pairing_request_id` unique, `hostname`,
  `os_name`, `os_version`, `display_name`, `credential_hash` unique, `status`
  one of `pending`/`active`/`failed`/`expired`, `created_at`, `expires_at`).
- `internal/secret`: `secret.Value` (prints `[hidden]`; `Reveal()`),
  `NewKey()`, `FromString(s)`, `Hash(v)`, `NormalizeCode(s)`.
- `internal/pairing`: start, poll (applies expiry when reading), and the
  approval endpoints, which answer `display_name` when `paired`.

### Proofs and errors

Proofs travel in `Authorization: Bearer <proof>`. Every error answer is
`{"error": "<code>"}`.

### Endpoint 3 — `POST /api/v1/pairings/current/code` (proof: polling key)

Request: `{"code": "4827-1934"}`. Unknown polling key →
`401 {"error": "unknown_key"}`. Otherwise `200` with one `result`:

| `result` | Other fields | When |
| --- | --- | --- |
| `accepted` | `credential`, `machine_id` | The code matches |
| `wrong_code` | `tries_left` | It does not match, and tries remain |
| `failed` | — | It does not match, and no tries remain |
| `expired` | — | The pairing is past its `expires_at` |
| `not_waiting_for_code` | `status` | The status is anything other than `waiting_for_code`, including `finishing` after an earlier correct code |

Compare `NormalizeCode(submitted)` with `NormalizeCode(stored)`, in constant
time. The code is only checked against the row found by this polling key
(R14).

**Correct code**, in one database transaction:

1. ```sql
   UPDATE pairing_requests SET status = 'finishing'
    WHERE id = $1 AND status = 'waiting_for_code' AND expires_at > now()
   RETURNING ...;
   ```
   If no row came back, do not create anything: read the row and answer
   `expired` or `not_waiting_for_code`. **This condition is what makes a
   second credential impossible.**
2. Make the credential with `secret.NewKey()`.
3. Insert the machine: `status = 'pending'`, `expires_at = now() + interval '5 minutes'`,
   `credential_hash = Hash(credential)`, `display_name` = the hostname (or
   `unknown` if empty), and the hostname and OS copied from the pairing request.
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

A row came back → `wrong_code` with the new `tries_left`, or `failed` if it
reached 0. No row → read the row and answer with its real state.

### Endpoint 4 — `POST /api/v1/machines/current/acknowledgment` (proof: credential)

Finds the machine by `credential_hash = Hash(credential)`. Unknown →
`401 {"error": "unknown_credential"}`. In one transaction:

```sql
UPDATE machines SET status = 'active'
 WHERE credential_hash = $1 AND status = 'pending' AND expires_at > now()
RETURNING pairing_request_id;
```

and, if a row came back, set that pairing request's status to `paired`.
Answer `{"result": "ok"}`.

If no row came back, read the machine: `active` → `{"result": "ok"}` again
(the acknowledgment is safe to repeat), `failed` → `{"result": "failed"}`,
`expired`, or `pending` past its `expires_at` → `{"result": "expired"}`.

### Endpoint 5 — `POST /api/v1/machines/current/save-failure` (proof: credential)

In one transaction: machine `pending` (and not expired) → `failed`, and its
pairing request → `failed` with `failure_reason = 'not_saved'`. Answer
`{"result": "ok"}`. If the machine was not `pending`, answer `failed` or
`expired` as above. Unknown → `401 unknown_credential`.

### Expiry applied when reading (extend task 02's and 03's reads)

With the database's clock: a pairing request in `finishing` whose machine is
`pending` and past its `expires_at` reads as `failed` with
`failure_reason = 'not_confirmed'`. A machine's credential is accepted only by
endpoints 4 and 5, and only while the machine is `pending` (or, for 4,
`active`).

### Expiry clean-up job

Started by `cmd/server`; runs every minute until the server stops. Each pass,
using the database's clock and conditional updates:

- pairing requests in `waiting_for_approval` or `waiting_for_code` past
  `expires_at` → `expired`;
- machines `pending` past `expires_at` → `expired`, and their pairing requests
  in `finishing` → `failed` with `failure_reason = 'not_confirmed'` (same
  transaction).

The job only makes stored statuses truthful; the checks above already refuse
expired records even if the job is late.

### Where the code goes

`internal/pairing/` for the endpoints, the reads, and the clean-up job;
`cmd/server/main.go` to register the three routes and start the job (add only
your own lines).

### Secrets in code

The polling key, pairing code, and credential stay in `secret.Value` until
written into a response, compared, or hashed. No log line, error message, or
test failure message may contain their values.

### Tests and the database

Tests that need PostgreSQL read `DATABASE_URL` (start it with
`docker compose up -d`). If the database cannot be reached from your
environment, stop and report it; do not skip those tests. Make the clean-up
job's interval a parameter so tests can run one pass directly.

## Boundaries

- May create or change: `internal/pairing/`, `cmd/server/main.go`.
- Must not change: `internal/db/migrations/`, `internal/secret/`,
  `internal/web/`, `slices/` except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestCorrectCodeIssuesCredential` — answer `accepted` with a 43-character credential and a `machine_id`; request is `finishing`; machine is `pending` with `expires_at` about 5 minutes ahead | R17 |
| `TestCodeFormatsAccepted` — `48271934` and `4827 1934` both match `4827-1934` | — |
| `TestCredentialOnlyHashed` — the raw credential appears in no column of either table; `credential_hash` equals `Hash` of the returned credential | R22 |
| `TestNoSecondCredential` — a second correct code answers `not_waiting_for_code` with `finishing`, and only one machine exists | — |
| `TestSimultaneousCorrectCodes` — 20 rounds of two correct codes sent at once: exactly one `accepted` and one machine per round | — |
| `TestWrongCodeCountsDown` — wrong codes give `tries_left` 4, 3, 2, 1, then `failed`; the request is `failed` with `wrong_codes`, and the approval read shows it | R13 |
| `TestSimultaneousWrongCodes` — 5 wrong codes sent at once never take `tries_left` below 0 or give more than 5 answers that count | R13 |
| `TestCodeOnlyForItsPairing` — pairing A's code sent with pairing B's polling key is a wrong code for B | R14 |
| `TestNoCodeWhenFinished` — for `expired`, `rejected`, and `failed` requests, even the right code answers `expired` or `not_waiting_for_code`, and creates no machine | R15, R16 |
| `TestAcknowledgment` — machine becomes `active`, request becomes `paired`, the approval read shows `paired` with `display_name` | R19, R27 |
| `TestAcknowledgmentRepeatable` — a second acknowledgment answers `ok` and changes nothing | — |
| `TestAcknowledgmentAfterExpiry` — with the machine's `expires_at` in the past, the acknowledgment answers `expired`, the machine never becomes `active`, and the request reads as `failed` / `not_confirmed` | R26 |
| `TestSaveFailure` — machine `failed`, request `failed` / `not_saved`; a later acknowledgment answers `failed` | R25 |
| `TestNotPairedUntilConfirmed` — while the machine is `pending`, no read anywhere answers `paired` | R27 |
| `TestCleanupPass` — one pass expires an old waiting request, expires an old pending machine, and fails its request with `not_confirmed`; it leaves fresh records alone | R15, R26 |
| `TestUnknownCredential` — an unknown credential gives `401 unknown_credential` on endpoints 4 and 5 | — |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The approval page (task 04), the worker (06, 07), the full end-to-end test (08).

## If anything is unclear

Stop and report the question. Do not guess.
