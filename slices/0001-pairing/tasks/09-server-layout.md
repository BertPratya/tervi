# Task 09 — Server layout

Slice: 0001-pairing    Risk: normal    Depends on: 01–08

## Goal

Move every server-only package under `internal/server/`, split the 622-line
`internal/pairing/pairing.go` into one file per topic, and write the SQL that
is repeated 4 times once. No behavior changes.

## Requirements (copied from the spec)

None. This task changes where code lives, not what it does.

## Context

### The layout rule (from `AGENTS.md`, "Code layout")

Code under `internal/server/` belongs to the server, code under
`internal/worker/` to the worker, and a package directly under `internal/`
is shared by both. `internal/server` itself is the server's assembly package:
settings, routes, start and stop. Each package is named after its feature;
each file holds one topic.

### Folder moves

Package names stay the same.

| Today | After |
| --- | --- |
| `internal/db` | `internal/server/db` |
| `internal/db/dbtest` | `internal/server/db/dbtest` |
| `internal/db/migrations/0001_pairing.sql` | `internal/server/db/migrations/0001_pairing.sql` (content unchanged) |
| `internal/pairing` | `internal/server/pairing` |
| `internal/web` (with `static/`) | `internal/server/web` |

Use `git mv`-style moves so file content stays the same where it can; the
architect commits.

### Files after this task (not counting tests)

```text
internal/server/config.go           unchanged
internal/server/server.go           New, Run
internal/server/host.go             requireKnownHost, requestHostName
internal/server/db/db.go            unchanged
internal/server/db/dbtest/dbtest.go unchanged except its import of db
internal/server/db/migrations/0001_pairing.sql
internal/server/pairing/pairing.go  Deps, Register, maxRequestBodyBytes
internal/server/pairing/start.go    start, startInput, startResponse, decodeStartInput
internal/server/pairing/poll.go     poll
internal/server/pairing/code.go     submitCode, codeInput, decodeCodeInput, issueCredential, writeCodeUnavailable
internal/server/pairing/machine.go  acknowledge, saveFailure, changeMachine
internal/server/pairing/approval.go readApproval, accept, reject, decide, approvalRow, queryApproval, approvalBody
internal/server/pairing/cleanup.go  StartCleanup, startCleanup, cleanupPass
internal/server/pairing/status.go   effectiveStatus, effectiveFailureReason (below)
internal/server/pairing/respond.go  bearerProof, writeJSON, writeError, writeInternalError
internal/server/web/web.go          unchanged
internal/server/web/static/         index.html, app.js, view.js, unchanged
```

**`host.go`.** Today the Host check is a closure inside `server.New`. Move it
into `func requireKnownHost(cfg Config, next http.Handler) http.Handler`,
which builds the allowed-host set from `cfg.PublicURL` and wraps `next`
exactly as the closure does today. `New` ends with
`return requireKnownHost(cfg, mux)`. `requestHostName` moves with it.

### The shared status SQL

The same SQL `CASE` that turns an old request into `expired` or `failed` is
written 4 times in `pairing.go`: in `poll`, `submitCode`,
`writeCodeUnavailable`, and `queryApproval`. The `failure_reason` `CASE` is
written twice: in `poll` and `queryApproval`. Write each once in `status.go`,
as Go constants, and build the queries from them:

```go
// effectiveStatus is the request's status as seen now: past its deadline, a
// waiting request counts as expired and an unconfirmed machine as failed.
// The query must name the tables pr and m, with LEFT JOIN machines AS m.
const effectiveStatus = `CASE
    WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
    WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
    ELSE pr.status
END`

// effectiveFailureReason is the failure reason that goes with effectiveStatus.
const effectiveFailureReason = `CASE WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now()
    THEN 'not_confirmed' ELSE coalesce(pr.failure_reason, '') END`
```

Join them into the query text with `+`. They are fixed text written by us,
never user input, so this is not SQL injection; every value still goes
through `$1`-style parameters.

### Tests

Move each test file with the code it tests. In tests, only `package` lines,
import paths, and qualified names may change. No test is removed, and no
assertion changes. `test/e2e/pairing_test.go` and `cmd/server/main_test.go`
change only their import paths.

## Boundaries

- May create or change: `internal/db`, `internal/pairing`, `internal/web`
  (moving out of them), `internal/server/`, import lines in `cmd/server/`
  and `test/e2e/`, and this task's row in `slices/0001-pairing/plan.md`.
- Must not change: `internal/worker/`, `internal/secret/`, `cmd/tervi/`,
  `slices/` (except that row), `docs/`, `AGENTS.md`, `.claude/`, `.codex/`,
  `scripts/`, the migration's content.
- Do not add dependencies.
- No behavior changes: no message, status code, JSON field, or SQL result
  changes.

## Definition of done

| Test | Proves |
| --- | --- |
| Every existing test passes after the move, with no assertion changed | No behavior changed |

Checks (run them and include the output in the report):

- `grep -c "WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code')" internal/server/pairing/*.go`
  → `1` for `status.go`, `0` for every other file.
- `grep -c "THEN 'not_confirmed'" internal/server/pairing/*.go` → `1` for
  `status.go`, `0` for every other file.
- `wc -l` on the non-test files in `internal/server/` and its subfolders:
  none over 300 lines.
- `go list ./internal/...` shows no package at `internal/db`,
  `internal/pairing`, or `internal/web`.

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- The worker's packages (task 10).
- The migrate command and the version check (task 11).
- Moving SQL into a separate layer (backlog B13).

## If anything is unclear

Stop and report the question. Do not guess.
