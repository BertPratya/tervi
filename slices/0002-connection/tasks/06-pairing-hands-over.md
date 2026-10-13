# Task 06 — Worker: pairing hands over to the daemon

Slice: 0002-connection    Risk: core    Depends on: 05

## Goal

Make `tervi pair` finish by starting tervi in the background and connecting,
check first that tervi is installed, and let a computer whose credential was
rejected pair again.

## Requirements (copied from the spec)

- R1. When pairing succeeds, the worker shall set `connect` to true in
  `state.json`.
- R5. When pairing succeeds, the worker shall set up the daemon as a systemd
  user service named `tervi`, start it, and enable it so that it starts every
  time the user logs in. The service shall run the same `tervi` program file
  that set it up.
- R10. When pairing succeeds, the terminal shall show `✓ Paired successfully.`,
  then `✓ tervi now runs in the background and starts when you log in.`, then
  `✓ Connected.` once the daemon is connected.
- R11. If the daemon has not connected within 30 seconds after pairing, then
  the terminal shall say that pairing is done but the connection is still
  being tried, show the last error, and print `Check with: tervi status`.
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`.
- R28. If `tervi pair` or `tervi connect` runs from a temporary program file
  (as `go run` creates), then it shall print
  `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi`
  and stop. `tervi pair` shall do this before contacting the server.
- R29. If the background service cannot be set up or started (for example,
  systemd is not available), then the worker shall say so with the reason,
  keep the pairing, and print `Try again with: tervi connect`.
- R31. When `tervi pair` or `tervi connect` has printed its last line, it
  shall exit and give the prompt back, leaving the daemon running in the
  background. Closing that terminal, or running other commands in it, shall
  not stop the daemon or its connection. The user works with the running
  daemon through `tervi` commands, such as `tervi status` and
  `tervi connect`.
- R33. When `tervi pair` runs on a computer whose credential was rejected
  (R18), the worker shall print
  `The server no longer accepts this computer's credential. Starting a new pairing.`,
  remove the old pairing (the secret store entry and the pairing in
  `state.json`), and start a new pairing. On a paired computer whose
  credential was not rejected, `tervi pair` shall still refuse, as in slice 1
  (R2 of slice 1).

## Context

### What exists (tasks 03–05)

- `state.State` has `Connect` and `StopReason`; only the daemon writes them.
  `StopReason` is `"credential_rejected"` after a rejection.
- `internal/worker/service`: the installed check (R28 message) and
  `EnsureRunning(ctx) error`.
- `internal/worker/control`: `Client.Connect(ctx)` with results
  `connected`, `already_connected`, `still_trying` (with the last error, or
  none if no try has failed yet), `rejected`, `not_paired`, `failed` (with
  the path of `state.json`).
- `internal/worker/cli` (task 05): `cli.Env` carries the program path, the
  systemd `Runner`, the service-file folder (`ServiceDir`), and the control
  client, so tests set fakes; an empty or nil one stops the hand-over with
  the internal-setting line; functions
  that print the two R29 lines and the two rejected lines. Reuse those
  functions; do not print the same messages a second way.
- `tervi pair` is handled in `internal/worker/cli` (step zero and the
  already-paired check), and the network part in `internal/worker/pair`,
  which prints `✓ Paired successfully.` and returns `0` on success, both for
  a new pairing (`StartNew`) and for finishing an earlier one
  (`FinishEarlier`).

### How R1 is met

By decision 1 in `plan.md`, only the daemon writes `connect`. Pairing meets
R1 by calling `control.Client.Connect`, which makes the daemon write
`connect: true`. If the service cannot start (R29), `connect` stays as it
is, and the printed `Try again with: tervi connect` is how the user finishes.

### Order in `tervi pair`

1. Parse the command as today (wrong usage still exits `2` first).
2. **Installed check (R28)**, before reading anything or contacting the
   server: the message, exit `1`. If the program path in `cli.Env` is empty,
   print `✗ tervi is missing an internal setting (<field name>).` and exit
   `1` before anything else; `TestMissingInternalSetting` covers this case
   for `tervi pair` too.
3. Step zero as today, with one change (R33): when `state.json` is confirmed,
   the secret store entry exists, and `stop_reason` is
   `"credential_rejected"`, print
   `The server no longer accepts this computer's credential. Starting a new pairing.`,
   delete the secret store entry and `state.json`, and continue as for an
   unpaired computer. If either delete fails, print the slice-1 message for
   that failure and exit `1`. Without that stop reason, the slice-1 refusal
   (`This computer is already paired with <server>. Nothing was changed.`)
   stays exactly as it is.
4. Pairing as today.
5. **Hand-over**, only when the pairing flow returned `0`:
   - `service.EnsureRunning(ctx)`. On failure: `✗ Can't start tervi in the background: <reason>`,
     `Try again with: tervi connect`, exit `1`. The pairing stays saved.
   - Print `✓ tervi now runs in the background and starts when you log in.`
   - `control.Client.Connect(ctx)`, then:

| Result | Output | Exit |
| --- | --- | --- |
| `connected`, `already_connected` | `✓ Connected.` | `0` |
| `still_trying` | `Paired, but not connected yet: <last error>. tervi keeps trying in the background.` then `Check with: tervi status` | `0` |
| `still_trying` with no last error (no try has failed yet) | `Paired, but not connected yet. tervi keeps trying in the background.` then `Check with: tervi status` (decided by the user during task 03) | `0` |
| `failed` (`state.json` could not be written) | `✗ Can't write <path>.` (decided by the user during task 03) | `1` |
| `rejected` | `✗ The server doesn't accept this computer's credential.` then `Pair again with: tervi pair --server <server>` | `1` |
| a control error | `✗ Can't reach tervi in the background: <reason>` then `Check with: tervi status` (task 05's function) | `1` |

   The whole hand-over takes at most 30 seconds plus the start step's 10
   seconds. Then the command exits; it never stops the daemon (R31).

Ctrl+C during the hand-over prints `Pairing finished. Connecting was cancelled; tervi keeps trying in the background.`
and exits `130`. The pairing stays saved.

## Boundaries

- May create or change: `internal/worker/cli/`, `internal/worker/pair/`
  only if the hand-over needs a small hook, their tests, and
  `test/e2e/pairing_test.go`. In every existing test that runs `tervi pair`
  (in `internal/worker/cli/cli_test.go`, `internal/worker/pair/`, and
  `test/e2e/pairing_test.go`), you may set a fake installed program path, a
  fake systemd `Runner`, a fake control client, and `ServiceDir` in
  `cli.Env`: the test binary itself lives inside `os.TempDir()`, so without
  fakes the installed check would stop it, and the hand-over would call real
  `systemctl`. Every test that reaches the hand-over sets `ServiceDir` to
  `t.TempDir()`, so no test ever writes `~/.config/systemd/user/tervi.service`.
- Must not change: `internal/worker/daemon/`, `internal/worker/control/`,
  `internal/worker/service/`, `internal/worker/state/`,
  `internal/worker/credential/`, `internal/server/`, `api/`, `design/`,
  `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPairInstalledCheckFirst` — a temporary program gets the R28 message and no request reaches the server | R28 |
| `TestMissingInternalSetting` — extended: `tervi pair` with an empty program path prints the internal-setting line and exits `1` before reading anything | R28 |
| `TestPairHandsOver` — after `✓ Paired successfully.`, the start steps run, then the two lines in order, exit `0`; the service file is written in the test's `t.TempDir()` `ServiceDir` and nowhere else | R5, R10 |
| `TestPairSetsConnectThroughDaemon` — the hand-over calls `Connect`, and the fake daemon wrote `connect: true` | R1 |
| `TestPairStillTrying` — the R11 lines, exit `0`; and with no last error, the same lines without the error | R11 |
| `TestPairConnectFailed` — `failed` prints `✗ Can't write <path>.`, exit `1`, and the pairing stays saved | R11 |
| `TestPairServiceFails` — the R29 lines, exit `1`, `state.json` and the secret store entry still hold the pairing | R29 |
| `TestPairFinishEarlierHandsOver` — finishing an earlier pairing also hands over | R5, R10 |
| `TestPairReturnsAfterLastLine` — the command returns while the fake daemon keeps running | R31 |
| `TestPairAgainAfterRejection` — with `stop_reason: credential_rejected`, the R33 line, both deleted, a new pairing starts | R33 |
| `TestAlreadyPairedStillRefused` — without that stop reason, the slice-1 refusal, nothing changed | R33 |
| `TestPairAgainDeleteFails` — a failed delete gives the slice-1 failure message and exit `1` | R33 |
| `TestHandOverCtrlC` — the message, exit `130`, pairing kept | R31 |
| `TestNoCredentialInPairOutput` — the output of every test above has no credential | R25 |

Existing slice-1 tests keep passing; change them only to add the fakes
above, and where the new hand-over lines now follow `✓ Paired successfully.`.

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- The end-to-end test (task 07).

## If anything is unclear

Stop and report the question. Do not guess.
