# Task 08 — Server: shared JSON helpers, and logging each `500`

Slice: 0002-connection    Risk: normal    Depends on: —

This task is numbered 08 because it was split out of task 01 during plan
review. It is built first: tasks 01 and 02 use what it adds.

## Goal

Move the server's JSON answer helpers out of the pairing package into one
shared package, so the new connection and machines routes reuse them instead
of copying them. While moving them, make every `500 internal_error` leave a
trace in the server log: what kind of operation failed, and nothing that
could hold a secret or a value.

## Requirements (copied from the spec)

- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`. *This task: the server log written for a
  `500`.*
- R34. When the server answers `internal_error`, it shall log what kind of
  operation failed, and never the error's details, a request value, a key,
  a credential, or a hash.

## Context

### What exists

`internal/server/pairing/respond.go` holds four unexported helpers:

```go
func bearerProof(r *http.Request) (string, bool)          // "Bearer <proof>", case-insensitive scheme
func writeInternalError(w http.ResponseWriter)            // 500 {"error": "internal_error"}
func writeError(w http.ResponseWriter, status int, code string)
func writeJSON(w http.ResponseWriter, status int, value any)
```

They are called about 55 times across `internal/server/pairing/*.go`.
`pairing.Deps` already has a `Logger *slog.Logger`.

### The new package: `internal/server/httpapi`

The server's JSON API conventions, shared by every server feature package.
Move the four helpers here as exported functions, with the same behavior:

```go
func BearerProof(r *http.Request) (string, bool)
func WriteJSON(w http.ResponseWriter, status int, value any)
func WriteError(w http.ResponseWriter, status int, code string)
func WriteInternalError(w http.ResponseWriter, logger *slog.Logger, kind string, err error)
```

Make `internal/server/pairing` use them, and delete `respond.go`. No answer
the server sends changes.

### Logging each `500` (R34, backlog B4)

`WriteInternalError` answers `500 {"error": "internal_error"}` as today, and
also logs:

```go
logger.Error("internal error", "kind", kind)
```

adding `"sqlstate", <code>` when `err` is (or wraps) a `*pgconn.PgError`.

- `kind` is a short fixed text naming the operation that failed, written by
  the caller, for example `"start pairing"`, `"read pairing"`,
  `"submit code"`, `"acknowledge machine"`. Each call site in `pairing`
  passes its own kind; choose one per operation, not per line.
- It never logs `err`'s message or detail, a request value, a key, a
  credential, or a hash. PostgreSQL error details can contain the values of
  a row, so the error itself is never printed.
- A nil `logger` logs nothing. A nil `err` logs the kind only.

## Boundaries

- May create or change: `internal/server/httpapi/`,
  `internal/server/pairing/` (only to use `httpapi`, pass the logger and a
  kind to each `WriteInternalError` call, and delete `respond.go`), their
  tests.
- Must not change: any answer the server sends, `internal/server/server.go`,
  `internal/server/web/`, anything under `internal/worker/`, `api/`,
  `design/`, `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestHTTPAPIHelpers` — `BearerProof` accepts only `Bearer <proof>` (any case of `Bearer`, one proof, nothing else); `WriteJSON` and `WriteError` set `Content-Type: application/json` and the status; `WriteInternalError` answers `500 {"error": "internal_error"}` | layout |
| `TestInternalErrorLogsKindOnly` — with a `*pgconn.PgError` whose message and detail hold a marker value, the log has the kind and the `sqlstate`, and neither marker; with a plain error, only the kind; with a nil logger, no panic and no log | R34, R25 |
| `TestPairingInternalErrorLogged` — a database failure in a pairing route (for example, a closed pool) answers `500` as before, and the server log has that route's kind text and no proof from the request | R34, R25 |

The slice-1 pairing tests, the end-to-end test, and the contract test pass
unchanged. `go vet ./...` and `go test ./...` pass. This task's row in
`plan.md` changes to `done`.

## Out of scope

- The connection route (task 01) and the machines list (task 02), which use
  these helpers.
- JSON answers for unknown paths and wrong methods (backlog B5).

## If anything is unclear

Stop and report the question. Do not guess.
