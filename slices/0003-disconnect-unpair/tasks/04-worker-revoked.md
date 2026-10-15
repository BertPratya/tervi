# Task 04 — Worker: a revoked credential, and the state without a daemon

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 01

## Goal

The daemon stops for good when the server closes its connection with
`4002` or answers `401 credential_revoked`, records the new stop reason
`credential_revoked`, and logs it. `tervi status` and `tervi connect` show
`This computer was unpaired on the server.` for it, and `tervi status`
shows `○ Disconnected.` for a computer whose `connect` is false, also when
no daemon is running.

## Requirements (copied from the spec)

- R7. While the user has disconnected, `tervi status` shall show
  `○ Disconnected.` then `  Connect with: tervi connect`, and exit with `1`,
  also when tervi is not running in the background.
  *This task: the message, from the daemon's answer or from `state.json`
  when no daemon runs. `tervi disconnect` itself is task 05.*
- R20. When the server refuses this computer's credential because it was
  revoked, the daemon shall stop trying, as in slice 2's R18, and write the
  log line
  `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server <server>`.
- R21. On such a computer, `tervi status`, `tervi connect`, and
  `tervi disconnect` shall print `✗ This computer was unpaired on the server.`
  then `  Pair it again with: tervi pair --server <server>`. `tervi status` and
  `tervi connect` shall exit with `1`; `tervi disconnect` with `0`, as in R5.
  *This task: `tervi status` and `tervi connect`. `tervi disconnect` is
  task 05.*

## Context

### What exists

- `internal/protocol` has `CloseRevoked = 4002` (task 01).
- The server answers a revoked credential with
  `401 {"error": "credential_revoked"}` on the connection route, and closes
  a connected machine with `4002` when it is revoked. Task 01 provides both
  the answer and the code; the routes that revoke (tasks 02, 03) may still
  be in progress, so this task's tests use a fake server.
- `internal/worker/daemon/errors.go` recognizes exactly
  `401 {"error": "unknown_credential"}` as a rejection
  (`isUnknownCredentialBody`). On it, `reject` writes `connect: false` and
  `stop_reason: "credential_rejected"`, sets the status, and logs one line.
- `waitForConnect` answers `rejected` when the status stops with
  `credential_rejected`.
- `tervi status` prints `✗ tervi isn't running in the background.` and
  `  Start it with: tervi connect` whenever the socket does not answer.

### The new stop reason: `internal/worker/state`

Add `StopReasonCredentialRevoked = "credential_revoked"` next to
`StopReasonCredentialRejected`. `design/data-model.md` already describes it.

### The daemon: `internal/worker/daemon/revoked.go` (new)

Put the revocation handling in the new file `daemon/revoked.go`; `Run` and
`tryConnection` only call it, so `daemon.go` stays under 300 lines.

- **Recognizing it.** A `401` whose body is exactly
  `{"error": "credential_revoked"}` (same strict parsing as today) is a
  revocation. A connection the server closes with `4002`, at any point after
  it opened, is a revocation too.
- **On a revocation**, the same as `reject` does today, with the other
  reason: write `connect: false` and `stop_reason: "credential_revoked"`
  (keeping every other field), set the status to `not_connecting` with that
  reason, stop trying, and log once:

  ```text
  ✗ This computer was unpaired on the server. Pair it again with: tervi pair --server <server>
  ```

  The daemon holds `daemon.lock`, so it is the writer (plan, "One writer").
  R20's line is the only line logged for a revocation: no
  `✗ Connection lost: …` after a `4002` close, and no `✗ … Next try in …`
  after a `401 credential_revoked`, so R20's line stays the last one.
- **`POST /connect`** works as after a rejection: it sets `connect: true`,
  clears the reason, and tries; a revocation on that try stops it again and
  the answer is the new result `revoked` (`daemon.ResultRevoked`), which
  the control socket passes through like `rejected`.
- `GET /status` already carries the stop reason; it now can be
  `credential_revoked`.

### `tervi status` and `tervi connect`: `internal/worker/cli`

Unpaired message (R21), two lines, used by both commands:

```text
✗ This computer was unpaired on the server.
  Pair it again with: tervi pair --server <server>
```

`<server>` is the address from `credential.json`.

`tervi status`:

| Daemon answers | Prints | Exit |
| --- | --- | --- |
| `not_connecting`, reason `credential_revoked` | the unpaired message | `1` |
| `not_connecting`, reason `credential_rejected` | as in slice 2 | `1` |
| `not_connecting`, no reason | `○ Disconnected.` then `  Connect with: tervi connect` (replaces slice 2's `○ Not connecting.`) | `1` |
| other states | as in slice 2 | as in slice 2 |

When the socket does not answer (no daemon), `tervi status` reads
`state.json` before printing anything:

| `state.json` | Prints | Exit |
| --- | --- | --- |
| `connect: false`, `stop_reason: "credential_revoked"` | the unpaired message | `1` |
| `connect: false`, `stop_reason: "credential_rejected"` | slice 2's rejection message | `1` |
| `connect: false`, no reason (also a missing `connect`) | `○ Disconnected.` / `  Connect with: tervi connect` | `1` |
| `connect: true` | slice 2's `✗ tervi isn't running in the background.` / `  Start it with: tervi connect` | `1` |

The pairing check before this (not paired, unreadable files) is unchanged.

`tervi connect`: the answer `revoked` prints the unpaired message and exits
with `1`. Everything else is as in slice 2.

## Boundaries

- May create or change: `internal/worker/daemon/`, `internal/worker/control/`
  (only passing `revoked` through), `internal/worker/cli/status.go`,
  `internal/worker/cli/connect.go`, `internal/worker/state/state.go`, their
  tests.
- Must not change: `internal/protocol/`, anything under `internal/server/`,
  `internal/worker/pair/`, `internal/worker/service/`, `api/`, `design/`,
  `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestDaemonStopsOnRevokedAnswer` — a `401 credential_revoked` stops trying, writes `connect: false` and `credential_revoked`, logs the line once | R20 |
| `TestDaemonStopsOnCloseRevoked` — a connection closed with `4002` stops the same way, with no further tries; R20's line is the last log line, with no "Connection lost" | R20 |
| `TestDaemonRevokedBodyStrict` — a `401` with another body is a failed try, as in slice 2; `unknown_credential` still means rejected | R20 |
| `TestConnectAfterRevoked` — `POST /connect` tries again and answers `revoked` when the server still refuses | R21 |
| `TestStatusRevoked` — the unpaired message and exit `1`, with and without a daemon | R21 |
| `TestStatusDisconnected` — `○ Disconnected.` and exit `1` from the daemon's answer, and from `state.json` with no daemon (also with `connect` missing) | R7 |
| `TestStatusNoDaemonConnectTrue` — slice 2's "isn't running" message when `connect` is true | R7 |
| `TestConnectRevokedMessage` — `tervi connect` prints the unpaired message and exits `1` | R21 |
| The no-secret tests from slice 2 still pass with the new lines | R21 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- `tervi disconnect` (task 05) and `tervi unpair` (task 06).
- `tervi pair` after a revocation (task 07).

## If anything is unclear

Stop and report the question. Do not guess.
