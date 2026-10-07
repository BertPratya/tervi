# Task 07 — Worker: code, saving, and confirmation

Slice: 0001-pairing    Risk: core    Depends on: 04, 06

## Goal

The second half of the worker's pairing flow: ask for the code, save the
credential in a safe order, confirm with the server (with retries), finish an
earlier unconfirmed pairing, and handle Ctrl+C after saving has begun.

## Requirements (copied from the spec)

- R8. The worker shall never show the pairing code. It appears only in the browser.
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
- R25. If saving the credential fails, then the worker shall report it, and the
  pairing shall fail; the browser shall show that it failed.
- R28. If the server's answer to the confirmation is lost, then the worker
  shall try the confirmation up to 3 times in total, showing each try. If every try fails,
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

### What exists (tasks 01, 05, 06)

- `internal/secret`: `secret.Value` prints `[hidden]`; `Reveal()`; `FromString(s)`.
- `internal/worker/local`: `Env{Stdin, Stdout, Stderr, Store *Store, StateDir}`,
  `Entry{Server, Credential}`, the store logic (`Store.Get`, `Store.Save` =
  set + read back + compare, `Store.Delete`), the exported read and write
  functions for `state.json` (`machine_id`, `credential_saved`, `confirmed`;
  safe writes), `StatePath(dir)` and `DeleteState(dir)` (a missing file is not
  an error), and step 0, which calls `Flow.FinishEarlier` for a saved,
  unconfirmed pairing whose server matches `--server`. Step 0 also removes
  leftover or damaged data by itself and starts a new pairing.
- A failed `state.json` delete prints `✗ Can't delete <path>.` then
  `  Check that you can change files in <folder>, then run the same command again.`
  (task 05's message), and exits `1`.
- The exported test backend `local.NewMemoryBackend()`: set `FailGet`,
  `FailSet`, `FailDelete`, or `ChangeOnRead` to make an operation fail;
  `Value(user)` shows what is stored. Use it as `local.NewStore(backend)`.
- `internal/worker/flow` (task 06): `New(opts)` with `Options{RequestTimeout,
  PollInterval, Now}`, starting and polling, Ctrl+C while starting and
  polling, and two placeholders this task replaces: the unexported function
  field `codePhase(ctx, env, server, pollingKey, triesLeft, deadline, shownExpiry) int`
  and `FinishEarlier`.
- Every message other than usage errors goes to **standard output**.

### The server's API (task 04)

| Call | Answer |
| --- | --- |
| `POST /api/v1/pairings/current/code`, polling key, `{"code"}` | `200` with `result`: `accepted` (+ `credential`, `machine_id`), `wrong_code` (+ `tries_left`), `failed`, `expired`, or `not_waiting_for_code` (+ `status`); `401 {"error": "unknown_key"}` |
| `POST /api/v1/machines/current/acknowledgment`, credential | `200` with `result`: `ok`, `failed`, or `expired`; `401 {"error": "unknown_credential"}` |
| `POST /api/v1/machines/current/save-failure`, credential | `200` with `result`: `ok`, `active`, `failed`, or `expired` |

Proofs go in `Authorization: Bearer <proof>`.

### The code phase — fill in `codePhase`

Print `Type the code shown in the browser (expires at <shownExpiry>, <triesLeft> tries left):`,
then the prompt `Code: `. Read one line from standard input; an empty line
asks again without sending. Never print the code itself (R8).

| Event | Output | Then |
| --- | --- | --- |
| The deadline passes while waiting for input | `✗ The code expired. Run the command again.` | Exit `1` |
| `wrong_code` | `✗ Wrong code. <n> tries left.` (n from the server) | Prompt again |
| `failed` | `✗ Too many wrong codes. Pairing failed. Run the command again.` | Exit `1` |
| `expired` | `✗ The code expired. Run the command again.` | Exit `1` |
| `not_waiting_for_code` | `✗ The pairing is no longer waiting for a code. Run the command again.` | Exit `1` |
| `401 unknown_key` | `✗ The server no longer knows this pairing. Run the command again.` | Exit `1` |
| No answer within 10 seconds, a connection failure, `5xx`, or a body that is not valid JSON | `✗ No answer from the server after sending the code. Run the same command again to start over.` **Never send the code again.** | Exit `1` |
| Any other answer | `✗ Unexpected answer from the server. Run the command again.` | Exit `1` |
| `accepted` | `✓ Code accepted.` | Saving |
| Ctrl+C while waiting for input, or while the code request is running | `Pairing cancelled. Nothing was saved.` Do not tell the server. | Exit `130` |
| Standard input ends (EOF) while waiting for a code, including at a later prompt after a wrong code | `✗ Input ended before a code was entered. Nothing was saved. Run the command again.` Do not tell the server. Stop at once; never wait for the deadline. | Exit `1` |
| Standard input cannot be read | `✗ Couldn't read the code. Nothing was saved. Run the command again.` | Exit `1` |

*The two input rows above are provisional: the architect chose them, and the
user has not confirmed them yet.* Text that ends without a newline (for
example `printf 0000 | tervi pair …`) still counts as a typed code and is sent;
the next prompt then finds the input ended and stops with the EOF row.

### Saving (after `accepted`)

Each write finishes before the next starts:

1. `state.json` ← `machine_id`, `credential_saved: false`, `confirmed: false`
2. `Store.Save(Entry{server, credential})` (save + read back + compare)
3. `state.json` ← `credential_saved: true`

**If write 2 fails:**

1. Show `✗ Couldn't save the credential. Pairing was not completed.`
2. Send the save-failure report once; its answer changes nothing.
3. Try `Store.Delete()`, since a save can write the entry and still fail its read-back.
4. If the delete worked (or there was nothing to delete), delete `state.json`;
   if that fails, also show the `state.json` delete message. If the entry
   delete failed, also show `✗ Couldn't remove the partly saved secret store entry.`
   and `  Make sure you are logged in to a desktop session and the keyring is unlocked, then run the same command again.`,
   and keep `state.json`; the next run cleans up what is left.
5. Exit `1`.

**If a `state.json` write fails** (`<path>` is the real path; the next run
recovers from what was written):

| Write | Output | Then |
| --- | --- | --- |
| Write 1 | `✗ Can't write <path>. Pairing was not completed.`; send the save-failure report once | Exit `1` |
| Write 3 | `✗ Can't write <path>.` then the finish command (below) | Exit `1` |
| `confirmed: true` after the server answered `ok` | `✗ The server confirmed the pairing, but this computer couldn't record it: can't write <path>.` then the finish command | Exit `1` |

The finish command is `  To finish, run:  tervi pair --server <server>`. In
the last two cases the next run sends the acknowledgment again, which is safe.

On success, show `✓ Credential saved.` and confirm.

### Confirming (also how an earlier pairing is finished)

Print `Confirming with the server...` and send the acknowledgment with the
credential to the **entry's server**, never to any other address.

| Result | Output | Then |
| --- | --- | --- |
| `ok` | `state.json` ← `confirmed: true`; `✓ Paired successfully.` | Exit `0` |
| No answer, connection failure, or `5xx` | Before try 2 and try 3: `  No answer, retrying (2 of 3)...` / `(3 of 3)`, one `opts.PollInterval` (2 seconds in `cmd/tervi`) after the previous try ended | After 3 failed tries: the message below, exit `1` |
| `expired` | Clean up (below); `✗ The pairing didn't finish in time. Run the same command again to start a new one.` | Exit `1` |
| `failed` | Clean up (below); `✗ The pairing failed. Run the same command again to start a new one.` | Exit `1` |
| `401 unknown_credential` | Clean up (below); `✗ The server doesn't recognize this pairing. Run the same command again to start a new one.` | Exit `1` |
| Any other answer | `✗ Unexpected answer from the server.` then the finish command below | Exit `1` |

**Clean up** = `Store.Delete()` first, then `DeleteState`. If the entry
cannot be deleted, show `✗ Can't delete the secret store entry.` and
`  Make sure you are logged in to a desktop session and the keyring is unlocked.`
**instead of** the message above, keep `state.json` (so the next run tries
again), and exit `1`. If `state.json` cannot be deleted, show the
`state.json` delete message instead, and exit `1`.

After 3 failed tries (`state.json` stays `confirmed: false`):

```text
✗ Saved, but couldn't confirm with the server.
  To finish, run:  tervi pair --server http://localhost:8080
```

### Finishing an earlier pairing — fill in `FinishEarlier`

Print `Finishing the earlier pairing with <entry server>...`, then confirm as
above using the entry, with the same clean-up. In this case the three messages
are:

| Answer | Message |
| --- | --- |
| `expired` | `✗ The earlier pairing didn't finish in time. Run the same command again to start a new one.` |
| `failed` | `✗ The earlier pairing failed. Run the same command again to start a new one.` |
| `401 unknown_credential` | `✗ The server doesn't recognize the earlier pairing. Run the same command again to start a new one.` |

### Ctrl+C after saving has begun

From write 1 onward, and during `FinishEarlier`, a cancelled `ctx` prints:

```text
Pairing cancelled before it was confirmed.
  To finish, run:  tervi pair --server <server>
```

where `<server>` is the server in standard form (the address the entry holds,
or is about to hold). Return `130`; do not tell the server. Before write 1,
the code phase's own Ctrl+C row applies (`Pairing cancelled. Nothing was saved.`).

### Secrets

The polling key and credential stay in `secret.Value` until sent or saved. No
output, log line, or test failure message may contain the polling key, the
credential, or the code the user typed.

### Where the code goes

`internal/worker/flow/`.

### Tests

Use a fake server built with `net/http/httptest`,
`local.NewStore(local.NewMemoryBackend())` (set its `Fail…` fields to make
operations fail), a temporary `StateDir` (made read-only to make a write
fail), short durations, a fixed `Now`, and a fake standard input.

## Boundaries

- May create or change: `internal/worker/flow/`.
- Must not change: `internal/worker/local/` or anything else, except this
  task's row in `slices/0001-pairing/plan.md`. If `local` lacks something the
  flow needs, stop and report it.
- Do not add dependencies.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPromptShowsSameExpiry` — the prompt's time equals the start screen's, with the tries left from the server | R12 |
| `TestCodeDeadline` — no input until the deadline: the code-expired message, exit `1` | R15 |
| `TestWrongCodes` — `wrong_code` shows the server's tries left; `failed` gives its message, exit `1` | R13 |
| `TestCodeAnswers` — `expired`, `not_waiting_for_code`, `401`, and an unexpected answer each give their message, exit `1` | Every answer defined |
| `TestCodeNeverResent` — no answer, `503`, and invalid JSON: the code was sent once only, the message, exit `1` | Never two credentials |
| `TestCodeNeverPrinted` — no output contains the typed code | R8 |
| `TestHappyPath` — order: `state.json` (false) → entry saved and read back → `state.json` (true) → acknowledgment → `confirmed: true`; `✓ Paired successfully.`, exit `0`; no file under `StateDir` contains the credential | R17, R18, R19 |
| `TestStateWriteFails` — each of the three `state.json` writes failing: its message, exit `1`, and the save-failure report only for write 1 | Defined failure |
| `TestSaveFails` — `Save` fails: the R20 message, one save-failure report, the entry deleted, `state.json` deleted, exit `1` | R20, R25 |
| `TestSaveFailsAndDeleteFails` — `Save` and `Delete` both fail: both messages and the hint, `state.json` kept, exit `1` | No stuck partial state |
| `TestAckRetries` — answers lost: 3 tries, each at least one `PollInterval` after the previous ended, each shown, the finish command, exit `1`, `state.json` still `confirmed: false` | R28 |
| `TestAckResults` — `expired`, `failed`, and `401` each delete the entry, then `state.json`, and show their own first-run message | R29 |
| `TestAckCleanupDeleteFails` — `expired` with `FailDelete`: the delete message and hint, `state.json` kept, exit `1` | No silent failure |
| `TestStateDeleteFails` — read-only `StateDir` after `Save` fails, and after `expired`: the entry deleted, the `state.json` delete message, exit `1` | No silent failure |
| `TestFinishEarlierPairing` — `FinishEarlier` with an entry: the acknowledgment goes to the entry's server, `confirmed: true`, exit `0` | R29, R30 |
| `TestFinishEarlierResults` — `FinishEarlier` answered `expired`, `failed`, and `401`: each "earlier" message in full, entry and `state.json` deleted, exit `1` | R29 |
| `TestCtrlCDuringCodePhase` — cancelling while waiting for input and while the code request is running: `Pairing cancelled. Nothing was saved.`, exit `130`, nothing written | R23 |
| `TestCtrlCAfterSaving` — cancelling after write 1 and during `FinishEarlier`: the not-confirmed message with the finish command, exit `130`, no further request | R23 |
| `TestNoSecretInOutput` — no output contains the polling key or the credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Starting and polling (task 06), step 0 (05), the server (01–04), the
end-to-end test (08).

## If anything is unclear

Stop and report the question. Do not guess.
