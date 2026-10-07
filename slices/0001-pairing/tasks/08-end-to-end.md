# Task 08 — End-to-end test

Slice: 0001-pairing    Risk: normal    Depends on: 03, 07

## Goal

Prove the slice works as a whole: the real server and the real worker,
talking over HTTP, with the browser's part played by calls to the approval
API. These tests are the evidence that slice 1 is done.

## Requirements (copied from the spec)

- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
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

- **The real server, importable** (`internal/server`, task 01):
  `New(cfg, pool, logger) http.Handler` with every route of the slice;
  `internal/db.Open` and `Migrate`; `internal/pairing.StartCleanup`. Needs
  `DATABASE_URL`.
- **The real worker, importable** (`internal/worker/local`, task 05):
  `Run(ctx, args, env, flow) int` with
  `Env{Stdin, Stdout, Stderr, Store, StateDir}`; an in-memory `Store` that can
  be told to fail any operation; `internal/worker/flow.New(opts)` (tasks 06,
  07) with short durations as options. Ctrl+C is a cancelled `ctx`.

### The approval API (the browser's part)

Proof: `Authorization: Bearer <approval key>`; the approval key is the part of
`approval_url` after `/pair/`.

| Call | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | The state: `status`, details, `pairing_code` while `waiting_for_code`, `failure_reason` when `failed`, `display_name` when `paired` |
| `POST /api/v1/approvals/current/accept` | The Pair button |
| `POST /api/v1/approvals/current/reject` | The Reject button |

### How the tests run

- Serve `server.New(...)` with `net/http/httptest` on `127.0.0.1`, against the
  PostgreSQL in `DATABASE_URL` (start it with `docker compose up -d`), with
  `PublicURL` set to the test server's address. Give it a logger writing into
  a buffer, so the test can read everything the server logged.
- Run `local.Run(ctx, []string{"pair", "--server", <address>}, env, flow.New(...))`
  in a goroutine, with the in-memory store, a temporary `StateDir`, short
  durations, standard output into a buffer, and a fake standard input the test
  writes to.
- Read the approval link from the worker's output, take the approval key, and
  drive the approval API as the page would.
- To lose an answer on purpose, put a small HTTP proxy between the worker and
  the server that forwards each request but drops the answer for the paths a
  test chooses.

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
| `TestPairingHappyPath` — start → Pair → read the code from the approval API → type it → `✓ Paired successfully.`, exit `0`; the approval read answers `paired` with the display name and OS; `state.json` says `confirmed: true`; the store holds the server and a credential | R19 |
| `TestRejectEndToEnd` — Reject → the worker prints the rejection and exits `1`; the approval read answers `rejected` | R9 |
| `TestFiveWrongCodesEndToEnd` — 5 wrong codes → the worker prints the failed message, exit `1`; the approval read answers `failed` with `wrong_codes` | R13 |
| `TestCtrlCAfterSavingThenFinish` — cancel `ctx` right after the credential is saved → the not-confirmed message, exit `130`; a second `Run` with the same `--server` finishes the pairing, exit `0` | R23 |
| `TestLostAcknowledgmentThenFinish` — the proxy drops the acknowledgment's answers → 3 tries shown, the finish command printed, exit `1`; a second `Run` finishes, exit `0` | R28 |
| `TestNoSecretEverPrinted` — across all tests above: the server's log contains no approval key, polling key, pairing code, or credential; the worker's output contains no polling key, pairing code, or credential, and the approval key only inside the printed link | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Changing any behavior. This task only tests what tasks 01–07 built.

## If anything is unclear

Stop and report the question. Do not guess.
