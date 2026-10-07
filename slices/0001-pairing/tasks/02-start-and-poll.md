# Task 02 — Server: start and poll

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Add the two endpoints the worker uses first: starting a pairing, and polling
its status.

## Requirements (copied from the spec)

- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting). *This task provides the link and the time left; the worker shows
  them in task 07.*
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and
  stop. *This task lets the worker read the status; rejecting is task 03.*
- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R22. The server shall store only hashes of the approval key, the polling key,
  and the credential. The pairing code may be stored as it is, because it is
  useless without the polling key and lives at most 10 minutes.

## Context

### What exists (task 01)

- Table `pairing_requests` with columns `id`, `hostname`, `os_name`,
  `os_version` (`text`, `''` means unknown), `status`, `polling_key_hash`,
  `approval_key_hash` (`bytea`, unique), `pairing_code`, `tries_left`
  (default 5), `failure_reason`, `created_at`, `expires_at`.
- Package `internal/secret`: `secret.Value` (prints as `[hidden]`; read with
  `Reveal()`), `NewKey()`, `FromString(s)`, `Hash(v)` (SHA-256 bytes).
- `cmd/server` reads `TERVI_PUBLIC_URL` (default `http://localhost:8080`) and
  holds a `*pgxpool.Pool`.

### Proofs

Proofs travel in the `Authorization: Bearer <proof>` header. A missing or
malformed header is treated like an unknown key. Every error answer is JSON:
`{"error": "<code>"}`.

### Endpoint 1 — `POST /api/v1/pairings` (no proof)

Request body (each field optional; unknown fields are ignored; body larger
than 4 KB → `400 {"error": "invalid_input"}`):

```json
{"hostname": "bert-desktop", "os_name": "Ubuntu", "os_version": "26.04"}
```

Limits, counted in characters (runes), not bytes: `hostname` ≤ 64,
`os_name` ≤ 64, `os_version` ≤ 32. A value over its limit →
`400 {"error": "invalid_input"}`, and nothing is stored. A missing field is
stored as `''`.

The server:

1. Makes a polling key and an approval key with `secret.NewKey()`.
2. Inserts a row with `status = 'waiting_for_approval'`, the two hashes, and
   `expires_at = now() + interval '10 minutes'`, using the **database's** clock.
3. Answers `201`:

```json
{
  "polling_key": "<43 characters>",
  "approval_url": "<TERVI_PUBLIC_URL>/pair/<approval key>",
  "expires_in_seconds": 600
}
```

The link's address comes only from `TERVI_PUBLIC_URL`, never from the
request's `Host` header.

### Endpoint 2 — `GET /api/v1/pairings/current` (proof: polling key)

Finds the row whose `polling_key_hash` equals `Hash(key)`. Unknown key →
`401 {"error": "unknown_key"}`.

Answers `200`:

```json
{"status": "waiting_for_approval", "expires_in_seconds": 412}
```

plus `"tries_left": 5` (the stored value) when the status is
`waiting_for_code`.

**Expiry is applied when reading**, with the database's clock: if
`expires_at <= now()` and the stored status is `waiting_for_approval` or
`waiting_for_code`, the answer's status is `expired`, even though the stored
status was not changed. `expires_in_seconds` is the whole seconds left,
rounded up, and never below 0.

### Where the code goes

- `internal/pairing/`: the handlers and their queries.
- `cmd/server/main.go`: register the two routes (Go 1.22 `ServeMux` patterns,
  for example `mux.HandleFunc("POST /api/v1/pairings", ...)`).

### Secrets in code

Keep keys in `secret.Value` until the moment they are written into the
response or hashed. No log line, error message, or test failure message may
contain a key's value.

### Tests and the database

Tests that need PostgreSQL read `DATABASE_URL` (start it with
`docker compose up -d`; the URL is in `.env.example`). If the database cannot
be reached from your environment, stop and report it; do not skip those tests.

## Boundaries

- May create or change: `internal/pairing/`, `cmd/server/main.go`.
- Must not change: `internal/db/migrations/`, `internal/secret/`, `slices/`
  except this task's row in `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestStartReturnsKeyLinkAndTimeLeft` — `201`, a 43-character polling key, a link `<TERVI_PUBLIC_URL>/pair/<43 characters>`, `expires_in_seconds` = 600 | R5 |
| `TestStartStoresOnlyHashes` — after a start, neither raw key appears in any column of the row; the stored hashes equal `Hash` of the returned values | R22 |
| `TestStartKeysAreUnique` — two starts never return the same polling key or approval key | R22 |
| `TestStartIgnoresHostHeader` — a forged `Host` header does not change the link | — |
| `TestStartLimits` — values at exactly 64/64/32 characters, including multi-byte ones such as Thai, are accepted; one more character gives `400` and stores nothing | — |
| `TestStartMissingFields` — an empty body `{}` is accepted and stored as `''` | — |
| `TestPollWaiting` — a fresh pairing polls as `waiting_for_approval` with `expires_in_seconds` between 595 and 600 | R9 |
| `TestPollUnknownKey` — an unknown or missing key gives `401 unknown_key` | — |
| `TestPollAfterExpiry` — after setting `expires_at` to the past with SQL, the poll answers `expired` while the stored status is still `waiting_for_approval` | R15 |
| `TestPollWaitingForCode` — after setting the status to `waiting_for_code` with SQL, the answer includes `tries_left: 5` | — |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Accepting and rejecting (task 03), the approval page (04), submitting the
code, the credential, and the expiry clean-up job (05), the worker (06, 07).

## If anything is unclear

Stop and report the question. Do not guess.
