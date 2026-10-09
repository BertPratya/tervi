# Task 10 — Worker layout

Slice: 0001-pairing    Risk: normal    Depends on: 09

## Goal

Replace the worker's vague package names (`local`, `flow`, `keyring`) with
one package per feature: `cli`, `pair`, `state`, and `credential`. No
behavior changes.

## Requirements (copied from the spec)

None. This task changes where code lives, not what it does.

## Context

### The layout rule (from `AGENTS.md`, "Code layout")

Code under `internal/worker/` belongs to the worker. Each package is named
after the feature it provides; generic names (`local`, `flow`, `util`,
`common`) are not allowed. Each file holds one topic.

### Package moves

| Today | After |
| --- | --- |
| `internal/worker/flow` | `internal/worker/pair` (package `pair`) |
| `internal/worker/local` | split into `internal/worker/cli`, `internal/worker/state`, `internal/worker/credential` |
| `internal/worker/keyring` | merged into `internal/worker/credential` |

### Files after this task (not counting tests)

```text
internal/worker/cli/cli.go           Env, Flow, command, parseCommand, Run, cancelled,
                                     printStoreReadFailure, printStoreDeleteFailure, printUnusableStore
internal/worker/cli/leftover.go      checkAndStart, runLeftover
internal/worker/pair/pair.go         the constants (default durations, API paths, maxResponseSize),
                                     Options, pairingFlow, New, newFlow, StartNew, FinishEarlier,
                                     printStartScreen, cancelPairing, output, cancelAfterSaving,
                                     printFinishCommand, printDeleteStateFailure, waitContext, normalizeContext
internal/worker/pair/client.go       requestResult, request, requestUntil, requestWithTimeout,
                                     bytesReader, startResponse, decodeStartResponse
internal/worker/pair/computer.go     computerInfo, computerDetails, parseOSRelease, truncate,
                                     displayOrUnknown, displayOS, and the readHostname/readOSRelease variables
internal/worker/pair/poll.go         poll, waitForNextPoll, reportOutage, pollResponse, decodePollResponse
internal/worker/pair/code.go         codeAnswer, inputLine, runCodePhase, readInputLines
internal/worker/pair/confirm.go      saveAndConfirm, saveFailed, reportSaveFailure, confirm,
                                     isTransientConfirmationFailure, cleanupAndReport
internal/worker/state/state.go       State, StatePath, ReadState, WriteState, DeleteState
internal/worker/state/address.go     StandardAddress
internal/worker/credential/store.go  ErrNotFound, ErrDamaged, Backend, Entry, entryJSON, Store, NewStore, Store's methods
internal/worker/credential/keyring.go  NewKeyringBackend, keyringBackend, the service constant
internal/worker/credential/memory.go   MemoryBackend, NewMemoryBackend (used only by tests)
```

A declaration not named above goes next to the function that uses it; one
used by several files goes in the package's main file (`cli.go`, `pair.go`,
`store.go`).

**Renamed identifiers:**

| Today | After |
| --- | --- |
| `keyring.New()` | `credential.NewKeyringBackend()` |
| keyring's `backend` type | `keyringBackend` |
| `local.Run`, `local.Env`, `local.Flow` | `cli.Run`, `cli.Env`, `cli.Flow` |
| `local.NewStore`, `local.Entry`, `local.Backend`, `local.NewMemoryBackend` | `credential.…` (same names) |
| `local.StatePath`, `ReadState`, `WriteState`, `DeleteState`, `State` | `state.…` (same names) |
| `local.StandardAddress` | `state.StandardAddress` |
| `flow.New`, `flow.Options` | `pair.New`, `pair.Options` |

**Imports after the split.** `cli` defines `Env` and the `Flow` interface.
`pair` imports `cli`, `state`, `credential`, and `secret`. `cli` imports
`state` and `credential`, never `pair`. `credential` imports `state` (its
`Save` and `Get` call `StandardAddress`) and `secret`. `cmd/tervi` connects
them. `cli.Env.Store` is a `*credential.Store`. No package imports another in
a loop.

**Names that would hide the `state` package.** Rename every local variable
named `state` in `internal/worker/`, in code and in tests, to `record`, so
the `state` package stays reachable. Today they include `local/run.go`
around line 104, `flow/finish.go` around lines 212 and 341,
`local/local_test.go` around line 408, and the
`state, exists, err := local.ReadState(...)` lines in `flow/finish_test.go`.
(`saved` is already used as a variable name in the tests, so don't use it.)

### Tests

Move each test file with the code it tests. Split
`internal/worker/local/local_test.go` across `cli`, `state`, and
`credential` by what each test checks. These edits are allowed in tests,
and nothing else:

- `package` lines, import paths, and qualified names.
- A shared test helper (for example `testCredential`) used by tests in two
  packages is copied into each package's test files.
- Renaming a local variable that would hide a package name.

No test is removed, and no assertion changes. `test/e2e/pairing_test.go`
changes only its imports and qualified names.

## Boundaries

- May create or change: `internal/worker/`, `cmd/tervi/`, imports and
  qualified names in `test/e2e/`, and this task's row in
  `slices/0001-pairing/plan.md`.
- Must not change: `internal/server/`, `internal/secret/`, `cmd/server/`,
  `slices/` (except that row), `docs/`, `AGENTS.md`, `.claude/`, `.codex/`,
  `scripts/`.
- Do not add dependencies.
- No behavior changes: no message, exit code, file format, or secret store
  format changes. `state.json` keeps its name and fields.

## Definition of done

| Test | Proves |
| --- | --- |
| Every existing test passes after the move, with no assertion changed | No behavior changed |

Checks (run them and include the output in the report):

- `wc -l` on the non-test files in `internal/worker/` and its subfolders:
  none over 300 lines. If one cannot get under 300 without splitting a single
  function, report it instead.
- `go list ./internal/...` shows no package at `internal/worker/local`,
  `internal/worker/flow`, or `internal/worker/keyring`.

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- The server's packages (task 09).
- The migrate command (task 11).
- Any item in `slices/backlog.md`.

## If anything is unclear

Stop and report the question. Do not guess.
