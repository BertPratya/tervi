# Task 06 — Worker: start and polling

Slice: 0001-pairing    Risk: core    Depends on: 02, 05

## Goal

The first half of the worker's pairing flow: read this computer's details,
start the pairing, show the start screen, and poll until the pairing is
approved, rejected, or expired, surviving network outages. Ctrl+C at any of
these moments stops cleanly.

## Requirements (copied from the spec)

- R4. If the server cannot be reached when starting, then the worker shall say
  so and stop.
- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting).
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R12. The worker shall ask for the code only after the pairing is approved,
  and shall show the same expiry time again. *This task decides when the code
  phase starts and keeps the shown time; task 07 shows the prompt.*
- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R23. When the user presses Ctrl+C, the worker shall stop at once. If nothing
  was saved yet, it shall say `Pairing cancelled. Nothing was saved.` If saving
  had begun, it shall say the pairing was not confirmed and show the command
  that finishes it, and the next run shall recover whatever was saved. A
  pairing that is not finished expires on its own. *This task covers the
  moments before anything is saved.*
- R24. If the connection to the server is lost while waiting for approval, then
  the worker shall show `Connection lost, retrying...`, keep trying until the
  deadline, and continue normally when the connection returns.

## Context

### What exists (tasks 01, 05)

- `internal/secret`: `secret.Value` prints `[hidden]`; `Reveal()`; `FromString(s)`.
- `internal/worker/local`: `Run(ctx, args, env, flow) int`, `Env{Stdin,
  Stdout, Stderr, Store, StateDir}`, `Entry{Server, Credential}`, and the
  interface:

  ```go
  type Flow interface {
      StartNew(ctx context.Context, env Env, server string) int
      FinishEarlier(ctx context.Context, env Env, entry Entry) int
  }
  ```

  `cmd/tervi/main.go` passes a placeholder flow and a `ctx` that is cancelled
  by Ctrl+C.

### The server's API (task 02)

| Call | Answer |
| --- | --- |
| `POST /api/v1/pairings` with `{"hostname", "os_name", "os_version"}` (each optional) | `201 {"polling_key", "approval_url", "expires_in_seconds"}` |
| `GET /api/v1/pairings/current`, `Authorization: Bearer <polling key>` | `200 {"status", "expires_in_seconds"}`, plus `"tries_left"` when `waiting_for_code`; `401 {"error": "unknown_key"}` |

### What to build

Package `internal/worker/flow`, with `func New(opts Options) local.Flow`.
`Options` holds the durations below, so tests run fast.

| Rule | Value |
| --- | --- |
| Every request waits at most | 10 seconds |
| Next poll starts after the previous one ended (answer, error, or timeout) | 2 seconds |

`StartNew` runs this task's half; when the pairing is approved, it calls an
internal `codePhase(ctx, env, server, pollingKey, triesLeft, deadline, shownExpiry) int`.
**In this task, `codePhase` prints `Code entry is not available yet.` and
returns `1`;** task 07 fills it in. `FinishEarlier` likewise prints
`Finishing is not available yet.` and returns `1` until task 07.

`cmd/tervi/main.go`: replace the placeholder flow with `flow.New(...)` using
the real durations.

### Reading this computer's details

- Hostname: `os.Hostname()`. OS: `NAME` and `VERSION_ID` from `/etc/os-release`.
- A value that cannot be read is left out of the request and shown as `unknown`.
- Truncate before sending: `hostname` and `os_name` to 64 characters,
  `os_version` to 32, counted in characters (runes). A value that fits stays
  whole; a longer one keeps its first (limit − 1) characters and ends with `…`.

### Starting

All messages go to standard output.

| Result | Output | Exit |
| --- | --- | --- |
| `201` | The start screen below, then polling | — |
| No connection within 10 seconds | `✗ Can't reach <server>. Check that the server is running, then run the command again.` | `1` |
| Sent, but no answer within 10 seconds | `✗ The server didn't answer, so the pairing could not be started. Run the command again.` **Never retried.** | `1` |
| Any other answer | `✗ The server refused to start a pairing (HTTP <code>).` | `1` |

Start screen (`<server>` is the standard form; the link exactly as received):

```text
Pairing this computer with http://localhost:8080
  Computer: bert-desktop
  OS:       Ubuntu 26.04

Open this link (on any device) and approve this computer:
  http://localhost:8080/pair/…

Waiting for approval... (expires at 14:32)
```

**Two times are kept:**

- the **shown expiry**: the worker's clock when the start answer arrived +
  `expires_in_seconds`, as local `HH:MM`. It never changes afterwards, so the
  code prompt shows exactly the same time (R12).
- the **deadline**: starts equal to the shown expiry; reset from
  `expires_in_seconds` in the poll answer that says `waiting_for_code`.

### Polling

| Poll result | Output | Then |
| --- | --- | --- |
| No answer, connection error, `5xx`, or a body that is not valid JSON — the first one of an outage | `Connection lost, retrying...` (once per outage) | Keep polling |
| First good answer after an outage | `Connection restored.` | Continue below |
| `waiting_for_approval` | — | Keep polling |
| `rejected` | `✗ Pairing was rejected in the browser.` | Exit `1` |
| `expired`, or the deadline passes during an outage | `✗ The link expired. Run the command again.` | Exit `1` |
| `401 unknown_key` | `✗ The server no longer knows this pairing. Run the command again.` | Exit `1` |
| Any other status (`finishing`, `paired`, `failed`) or other `4xx` | `✗ Unexpected answer from the server. Run the command again.` | Exit `1` |
| `waiting_for_code` | `✓ Approved in the browser.` | Stop polling; reset the deadline; call `codePhase` |

Polls never overlap: the next starts 2 seconds after the previous ended.

### Ctrl+C

`ctx` is cancelled by Ctrl+C. At any moment in this task's half (starting or
polling), print `Pairing cancelled. Nothing was saved.` and return `130`. Do
not tell the server.

### Secrets

Keep the polling key in `secret.Value`. The approval link is printed, which is
its purpose; nothing else may print the approval key, and no output, log line,
or test failure message may contain the polling key.

### Tests

Use a fake server built with `net/http/httptest`, a temporary `StateDir`, the
in-memory store from task 05, and short durations.

## Boundaries

- May create or change: `internal/worker/flow/`, `cmd/tervi/main.go`.
- Must not change: `internal/worker/local/` (except small exported additions
  the flow needs, listed in your report), everything else except this task's
  row in `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestStartScreen` — server, computer, OS, the link as received, and the expiry computed from `expires_in_seconds` | R5 |
| `TestUnknownAndTruncatedDetails` — unreadable values are omitted and shown as `unknown`; a 65-character hostname is sent as 63 characters + `…` | R5 |
| `TestCannotReach` — no server: the R4 message, exit `1`, nothing written | R4 |
| `TestStartNoAnswerNotRetried` — the server takes the request but never answers: one request only, the message, exit `1` | A start is never retried |
| `TestRejected` — `rejected`: the message within one poll interval, exit `1` | R9 |
| `TestConnectionLostAndRestored` — failures (no answer, `503`, invalid JSON) show `Connection lost, retrying...` once, then `Connection restored.`, and polling continues | R24 |
| `TestDeadlineDuringOutage` — the server stays down past the deadline: the link-expired message, exit `1` | R15, R24 |
| `TestPollsNeverOverlap` — the fake server never sees two polls at once, and they are at least one interval apart | Polling rule |
| `TestUnexpectedAnswers` — `finishing`, `paired`, `failed`, and `404` while polling: the unexpected-answer message, exit `1`; `401`: its own message | Every answer defined |
| `TestApprovedHandsOver` — `waiting_for_code`: `✓ Approved in the browser.`, `codePhase` is called with the polling key, `tries_left`, a reset deadline, and the unchanged shown expiry | R12 |
| `TestCtrlCBeforeSaving` — cancelling `ctx` while starting and while polling: `Pairing cancelled. Nothing was saved.`, exit `130`, nothing written, no further request | R23 |
| `TestNoSecretInOutput` — no output contains the polling key; the approval key appears only inside the printed link | Secrets never printed |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The code prompt, saving, the acknowledgment, finishing an earlier pairing,
and Ctrl+C after saving (task 07). The server (01–04). Step 0 (05).

## If anything is unclear

Stop and report the question. Do not guess.
