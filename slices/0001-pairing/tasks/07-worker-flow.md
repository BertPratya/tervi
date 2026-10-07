# Task 07 — Worker: pairing flow

Slice: 0001-pairing    Risk: core    Depends on: 02, 05, 06

## Goal

The worker's side of pairing over the network: start, poll, ask for the
code, save the credential in the right order, confirm with the server, and
handle every failure and Ctrl+C with an exact message and exit code.

## Requirements (copied from the spec)

- R4. If the server cannot be reached when starting, then the worker shall say
  so and stop.
- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting).
- R8. The worker shall never show the pairing code. It appears only in the browser.
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R12. The worker shall ask for the code only after the pairing is approved,
  and shall show the same expiry time again.
- R13. If a wrong code is typed, then the worker shall show how many tries are
  left. After 5 wrong codes, the pairing shall fail, and both the terminal and
  the browser shall say so.
- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R17. When the correct code is typed, the worker shall receive its credential.
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs.
- R19. When the credential is saved, the worker shall confirm with the server.
  Then the terminal shall show `✓ Paired successfully.`, and the browser shall
  show that the computer is paired, with its name and OS, and that the tab can
  be closed.
- R20. If saving the credential fails, then the worker shall say that pairing
  was not completed.
- R23. When the user presses Ctrl+C, the worker shall stop at once. If nothing
  was saved yet, it shall say `Pairing cancelled. Nothing was saved.` If saving
  had begun, it shall say the pairing was not confirmed and show the command
  that finishes it, and the next run shall recover whatever was saved. A
  pairing that is not finished expires on its own.
- R24. If the connection to the server is lost while waiting for approval, then
  the worker shall show `Connection lost, retrying...`, keep trying until the
  deadline, and continue normally when the connection returns.
- R25. If saving the credential fails, then the worker shall report it, and the
  pairing shall fail; the browser shall show that it failed.
- R28. If the server's answer to the confirmation is lost, then the worker
  shall retry the confirmation 3 times, showing each try. If every try fails,
  the worker shall say that the credential is saved but not confirmed, and
  show the full command that finishes the pairing.
- R29. When the command runs again after a pairing that was saved but not
  confirmed, the worker shall finish that pairing instead of starting a new
  one. If that pairing already failed or expired, the worker shall say so,
  remove what it saved, and the next run shall start a new pairing.
- R30. If the command names a different server while a pairing is saved but
  not confirmed, then the worker shall change nothing, say which server the
  unfinished pairing belongs to, and show the command that finishes it. The
  credential shall never be sent to any server other than the one that issued
  it: the server address is saved with the credential in the OS secret store,
  and only that saved address decides where the credential may go.

## Context

### What exists

- **Task 06** (`internal/worker/local`, `cmd/tervi`): the command line, the
  address standard form, the secret store interface (save = save + read back
  + compare; get; delete) with an in-memory implementation for tests,
  `state.json` (`machine_id`, `credential_saved`, `confirmed`; safe writes),
  and step 0, which returns either **start a new pairing** or **finish the
  earlier pairing** for this task to carry out. `cmd/tervi` currently prints
  `Pairing is not available yet.` for those two; replace that.
- **Task 01** (`internal/secret`): `secret.Value` prints as `[hidden]`;
  `Reveal()` gives the value; `FromString(s)` wraps one.

### The server's API (tasks 02, 05)

Proofs go in `Authorization: Bearer <proof>`. Error answers are
`{"error": "<code>"}`.

| Call | Request | Answer |
| --- | --- | --- |
| `POST /api/v1/pairings` | `{"hostname", "os_name", "os_version"}`, each optional | `201 {"polling_key", "approval_url", "expires_in_seconds"}` |
| `GET /api/v1/pairings/current` (polling key) | — | `200 {"status", "expires_in_seconds"}` + `"tries_left"` when `waiting_for_code`; `401 unknown_key` |
| `POST /api/v1/pairings/current/code` (polling key) | `{"code"}` | `200 {"result": "accepted", "credential", "machine_id"}`, `{"result": "wrong_code", "tries_left"}`, `{"result": "failed"}`, `{"result": "expired"}`, or `{"result": "not_waiting_for_code", "status"}`; `401 unknown_key` |
| `POST /api/v1/machines/current/acknowledgment` (credential) | — | `200 {"result": "ok"}`, `{"result": "expired"}`, or `{"result": "failed"}`; `401 unknown_credential` |
| `POST /api/v1/machines/current/save-failure` (credential) | — | `200 {"result": "ok"}` (or `expired` / `failed`) |

Statuses from polling: `waiting_for_approval`, `waiting_for_code`,
`finishing`, `paired`, `rejected`, `failed`, `expired`.

### Timing rules

| Rule | Value |
| --- | --- |
| Every request waits at most | 10 seconds |
| Next poll starts after the previous one ended (answer, error, or timeout) | 2 seconds |
| Acknowledgment: tries, and the pause between them | 3 tries, 2 seconds |

Make these durations parameters so tests run fast.

**The worker's deadline** = its own clock when the answer arrived +
`expires_in_seconds`. It is set from the start answer, and set again from the
poll answer that says `waiting_for_code`. Shown as the local clock time
`HH:MM`.

### Reading this computer's details

- Hostname: `os.Hostname()`.
- OS: `NAME` and `VERSION_ID` from `/etc/os-release`.
- A value that cannot be read is left out of the request and shown as `unknown`.
- Truncate before sending: `hostname` and `os_name` to 64 characters,
  `os_version` to 32, counted in characters (runes). A value that fits stays
  whole; a longer one keeps its first (limit − 1) characters and ends with
  `…`, so the result is exactly at the limit.

### Starting a new pairing

Send the start request to `--server` (standard form). Then:

| Result | Output | Exit |
| --- | --- | --- |
| `201` | The start screen below, then polling | — |
| No connection within 10 seconds | `✗ Can't reach <server>. Check that the server is running, then run the command again.` | `1` |
| Sent, but no answer within 10 seconds | `✗ The server didn't answer, so the pairing could not be started. Run the command again.` Never retried. | `1` |
| Any other answer | `✗ The server refused to start a pairing (HTTP <code>).` | `1` |

Start screen:

```text
Pairing this computer with http://localhost:8080
  Computer: bert-desktop
  OS:       Ubuntu 26.04

Open this link (on any device) and approve this computer:
  <approval_url exactly as received>

Waiting for approval... (expires at 14:32)
```

### Polling

| Poll result | Output | Then |
| --- | --- | --- |
| First failure (instant error or 10-second timeout) | `Connection lost, retrying...` (once per outage) | Keep polling |
| First success after failures | `Connection restored.` | Keep polling |
| `waiting_for_approval` | — | Keep polling |
| `rejected` | `✗ Pairing was rejected in the browser.` | Exit `1` |
| `expired`, or the deadline passes during an outage | `✗ The link expired. Run the command again.` | Exit `1` |
| `401 unknown_key` | `✗ The server no longer knows this pairing. Run the command again.` | Exit `1` |
| `waiting_for_code` | `✓ Approved in the browser.` then `Type the code shown in the browser (expires at 14:32, 5 tries left):` | Stop polling; the code prompt |

### The code prompt

The prompt is `Code: `. Read one line from standard input; an empty line asks
again without sending. Never print the code itself (R8).

| Event | Output | Then |
| --- | --- | --- |
| The deadline passes while waiting for input | `✗ The code expired. Run the command again.` | Exit `1` |
| `wrong_code` | `✗ Wrong code. <n> tries left.` | Prompt again |
| `failed` | `✗ Too many wrong codes. Pairing failed. Run the command again.` | Exit `1` |
| `expired` | `✗ The code expired. Run the command again.` | Exit `1` |
| `not_waiting_for_code` | `✗ The pairing is no longer waiting for a code. Run the command again.` | Exit `1` |
| No answer, or the connection fails | `✗ No answer from the server after sending the code. Run the same command again to start over.` **Never send the code again.** | Exit `1` |
| `accepted` | `✓ Code accepted.` | Saving |

### Saving (after `accepted`)

Each write finishes before the next starts:

1. `state.json` ← `machine_id`, `credential_saved: false`, `confirmed: false`
2. Secret store entry ← `{server, credential}` (save + read back + compare)
3. `state.json` ← `credential_saved: true`

If write 2 fails: show `✗ Couldn't save the credential. Pairing was not
completed.`, send the save-failure report once (its result does not change the
output), delete `state.json`, exit `1`. On success: `✓ Credential saved.`, then
the acknowledgment.

### The acknowledgment (also used to finish an earlier pairing)

Print `Confirming with the server...`. Send the acknowledgment with the
credential to the **server saved in the entry**, never to any other address.

| Result | Output | Then |
| --- | --- | --- |
| `ok` | `state.json` ← `confirmed: true`; `✓ Paired successfully.` | Exit `0` |
| No answer, connection failure, or `5xx` | `  No answer, retrying (2 of 3)...` / `(3 of 3)` before tries 2 and 3 | After 3 failed tries: the message below, exit `1` |
| `expired`, `failed`, or `401 unknown_credential` | Delete `state.json` and the entry; `✗ The earlier pairing didn't finish in time. Run the same command again to start a new one.` | Exit `1` |

After 3 failed tries:

```text
✗ Saved, but couldn't confirm with the server.
  To finish, run:  tervi pair --server http://localhost:8080
```

To **finish an earlier pairing** (step 0's outcome), print
`Finishing the earlier pairing with <saved server>...` and run the
acknowledgment above with the saved entry.

### Ctrl+C

Stop at once (use `signal.NotifyContext`); do not tell the server.

| Pressed during | Output | Exit |
| --- | --- | --- |
| Starting, polling, or the code prompt (before write 1) | `Pairing cancelled. Nothing was saved.` | `130` |
| From write 1 onward, including finishing an earlier pairing | `Pairing cancelled before it was confirmed.` then `To finish, run:  tervi pair --server <saved server>` | `130` |

### Output and secrets

Messages go to standard output; usage errors stay as task 06 made them. The
polling key and credential stay in `secret.Value` until sent or saved. No
output, log line, or test failure message may contain the polling key, the
credential, or the pairing code.

### Where the code goes

`internal/worker/flow/` for the flow; `cmd/tervi/main.go` to connect step 0's
two outcomes to the flow and set the exit code.

### Tests

Use a fake server built with `net/http/httptest` that answers like the API
above, the in-memory secret store from task 06, a temporary folder for
`state.json`, short durations, and a fake standard input.

## Boundaries

- May create or change: `internal/worker/flow/`, `cmd/tervi/`.
- Must not change: `internal/worker/local/` (except small exported additions
  the flow needs, listed in your report), the server's packages, `slices/`
  except this task's row in `slices/0001-pairing/plan.md`.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestStartScreen` — shows server, computer, OS, the link exactly as received, and the expiry time computed from `expires_in_seconds` | R5 |
| `TestUnknownAndTruncatedDetails` — unreadable values are omitted and shown as `unknown`; a 65-character hostname is sent as 63 characters + `…` | R5 |
| `TestCannotReach` — no server: the R4 message, exit `1`, nothing written | R4 |
| `TestStartNoAnswerNotRetried` — the server takes the request but never answers: one request only, the message, exit `1` | — |
| `TestRejected` — `rejected` while polling: the message within one poll interval, exit `1` | R9 |
| `TestConnectionLostAndRestored` — failures show `Connection lost, retrying...` once, then `Connection restored.`, then pairing continues | R24 |
| `TestDeadlineDuringOutage` — the server stays down past the deadline: the link-expired message, exit `1` | R15, R24 |
| `TestPollsNeverOverlap` — the fake server never sees two polls at once, and polls are at least one interval apart | — |
| `TestCodeOnlyAfterApproval` — the prompt appears only after `waiting_for_code`, with the expiry time and tries left | R12 |
| `TestCodeDeadline` — no input until the deadline: the code-expired message, exit `1` | R15 |
| `TestWrongCodes` — `wrong_code` shows the tries left from the server; `failed` gives the failed message and exit `1` | R13 |
| `TestCodeNeverResent` — the code request gets no answer: sent once only, the message, exit `1` | — |
| `TestCodeNeverPrinted` — no output contains the code typed | R8 |
| `TestHappyPath` — saving order is `state.json` (false) → entry (read back) → `state.json` (true) → acknowledgment → `confirmed: true`, `✓ Paired successfully.`, exit `0` | R17, R18, R19 |
| `TestSaveFails` — the entry cannot be saved: the R20 message, one save-failure report, `state.json` removed, exit `1` | R20, R25 |
| `TestAckRetries` — the acknowledgment answers are lost: 3 tries 2 intervals apart, each shown, then the finish command, exit `1`, `state.json` still `confirmed: false` | R28 |
| `TestFinishEarlierPairing` — step 0's finish outcome: acknowledgment to the saved server, `confirmed: true`, exit `0` | R29 |
| `TestFinishExpired` — the acknowledgment answers `expired`: entry and `state.json` deleted, the message, exit `1` | R29 |
| `TestCredentialOnlyToSavedServer` — while finishing, the acknowledgment goes only to the entry's server | R30 |
| `TestCtrlCBeforeSaving` / `TestCtrlCAfterSaving` — each message, exit `130`, nothing sent to the server | R23 |
| `TestNoSecretInOutput` — across all tests, no output contains the polling key or the credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The server (tasks 01–05), the page (04), step 0's local decisions (06), the
end-to-end test with a real server (08).

## If anything is unclear

Stop and report the question. Do not guess.
