# Task 08 — End-to-end test

Slice: 0001-pairing    Risk: normal    Depends on: 04, 07

## Goal

Prove the slice works as a whole: a real server and the real worker flow,
talking over HTTP, with the browser's part played by calls to the approval
API. These tests are the evidence that slice 1 is done.

## Requirements (copied from the spec)

- R13. If a wrong code is typed, then the worker shall show how many tries are
  left. After 5 wrong codes, the pairing shall fail, and both the terminal and
  the browser shall say so.
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs.
- R19. When the credential is saved, the worker shall confirm with the server.
  Then the terminal shall show `✓ Paired successfully.`, and the browser shall
  show that the computer is paired, with its name and OS, and that the tab can
  be closed.
- R23. When the user presses Ctrl+C, the worker shall stop at once. If nothing
  was saved yet, it shall say `Pairing cancelled. Nothing was saved.` If saving
  had begun, it shall say the pairing was not confirmed and show the command
  that finishes it, and the next run shall recover whatever was saved. A
  pairing that is not finished expires on its own.
- R28. If the server's answer to the confirmation is lost, then the worker
  shall retry the confirmation 3 times, showing each try. If every try fails,
  the worker shall say that the credential is saved but not confirmed, and
  show the full command that finishes the pairing.

## Context

### What exists

- **Server** (`cmd/server`, `internal/pairing`, `internal/web`,
  `internal/db`): every endpoint of the slice, the approval page, and the
  expiry clean-up job. Needs `DATABASE_URL`.
- **Worker** (`cmd/tervi`, `internal/worker/local`, `internal/worker/flow`):
  step 0 and the full flow, with parameters for its durations, an in-memory
  secret store for tests, and a settable folder for `state.json`.

### The approval API (the browser's part)

Proof: `Authorization: Bearer <approval key>`, where the approval key is the
last part of `approval_url`.

| Call | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | The current state: `status`, details, `pairing_code` while `waiting_for_code`, `failure_reason` when `failed`, `display_name` when `paired` |
| `POST /api/v1/approvals/current/accept` | Accept |
| `POST /api/v1/approvals/current/reject` | Reject |

### How the tests run

- Start the real server in the test process on `127.0.0.1` with a free port,
  against the PostgreSQL in `DATABASE_URL` (start it with
  `docker compose up -d`), with `TERVI_PUBLIC_URL` set to that address.
- Run the real worker flow in the same process, with the in-memory secret
  store, a temporary folder for `state.json`, short durations, and a fake
  standard input that types what the test decides.
- Read the approval link from the worker's output, take the approval key from
  it, and drive the approval API like the page would.
- To lose an answer on purpose, put a small HTTP proxy between the worker and
  the server that forwards requests but drops the answer for the paths a test
  chooses.
- Capture everything the server logs and everything the worker prints.

If the database cannot be reached from your environment, stop and report it;
do not skip these tests.

## Boundaries

- May create or change: `test/e2e/`.
- Must not change: any other folder. If a test finds a bug in another task's
  code, stop and report it with the failing test; do not fix it here.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPairingHappyPath` — start → accept → read the code from the approval API → type it → `✓ Paired successfully.`, exit `0`; the approval read answers `paired` with the display name and OS; `state.json` says `confirmed: true`; the entry holds the server and credential | R19 |
| `TestRejectEndToEnd` — reject → the worker prints the rejection and exits `1`; the approval read answers `rejected` | — |
| `TestFiveWrongCodesEndToEnd` — 5 wrong codes → the worker prints the failed message, exit `1`; the approval read answers `failed` with `wrong_codes` | R13 |
| `TestCtrlCAfterSavingThenFinish` — cancel right after the credential is saved → the not-confirmed message and exit `130`; a second run with the same `--server` finishes the pairing and exits `0` | R23 |
| `TestLostAcknowledgmentThenFinish` — the proxy drops the acknowledgment's answers → 3 tries shown, the finish command printed, exit `1`; a second run finishes, exit `0` | R28 |
| `TestNoSecretEverPrinted` — across all tests above, neither the captured server log nor the worker's output contains any approval key, polling key, pairing code, or credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Changing any behavior. This task only tests what tasks 01–07 built.

## If anything is unclear

Stop and report the question. Do not guess.
