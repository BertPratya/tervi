# Task 05 — Worker: command and local state

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Everything the worker does on its own computer before talking to the server:
the command line, the server address, the secret store logic, `state.json`,
and deciding what to do from them (step 0). Also an importable `Run`, so the
end-to-end test can run the real worker, and an in-memory secret store backend
that every later worker test uses. The network flow, the real keyring backend,
and `cmd/tervi` are tasks 06 and 07.

## Requirements (copied from the spec)

- R1. When `tervi pair` runs without `--server`, the worker shall show
  `Usage: tervi pair --server <url>` and stop.
- R2. If this computer is already paired, then the worker shall say which
  server it is paired with, change nothing, and stop.
- R3. If the OS secret store cannot be used, then the worker shall say so and
  stop, before contacting the server.
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs.
- R23. When the user presses Ctrl+C, the worker shall stop at once. If nothing
  was saved yet, it shall say `Pairing cancelled. Nothing was saved.` If saving
  had begun, it shall say the pairing was not confirmed and show the command
  that finishes it, and the next run shall recover whatever was saved. A
  pairing that is not finished expires on its own. *This task covers Ctrl+C
  during step 0.*
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
- R31. If the local pairing data is incomplete or damaged (the state file
  without the secret store entry, the entry without the state file, or an
  entry that cannot be read as pairing data), then the worker shall remove
  what is left, say what it found and removed, and start a new pairing. If
  removing fails, then the worker shall say so and stop.

## Context

### What exists (task 01)

`internal/secret`: `secret.Value` holds a secret and prints as `[hidden]`
everywhere, including `json.Marshal`. `Reveal()` returns the real string;
`FromString(s)` wraps one.

### Output streams

Usage errors go to standard error. **Every other message goes to standard
output.**

### `Run` — the importable worker

In `internal/worker/local`:

```go
type Env struct {
    Stdin          io.Reader
    Stdout, Stderr io.Writer
    Store          *Store   // the secret store logic (below)
    StateDir       string   // the folder holding state.json
}

type Flow interface {
    // StartNew runs a new pairing with server (standard form) and returns the exit code.
    StartNew(ctx context.Context, env Env, server string) int
    // FinishEarlier finishes a saved, unconfirmed pairing and returns the exit code.
    FinishEarlier(ctx context.Context, env Env, entry Entry) int
}

func Run(ctx context.Context, args []string, env Env, flow Flow) int
```

`Run` parses `args`, runs step 0, and calls `flow` for the two outcomes that
need the server. It returns the exit code. Ctrl+C reaches the worker as a
cancelled `ctx`.

**Ctrl+C during step 0:** before each step 0 operation, and after each one
returns, `Run` checks `ctx`. If it is cancelled, `Run` stops at once, does
nothing further, and returns `130`, printing:

- `Pairing cancelled before it was confirmed.` and
  `  To finish, run:  tervi pair --server <saved server>`, once step 0 has
  found a well-formed, unconfirmed entry whose server equals `--server`;
- `Pairing cancelled. Nothing was saved.` at every other moment.

(An operation already waiting on the keyring cannot be interrupted; the check
happens when it returns.)

### The command line

The only command is `tervi pair --server <url>`. Usage errors go to standard
error and return exit code `2`, before anything is read or written:

| Input | Output |
| --- | --- |
| `--server` missing, or `--server` with no value | `Usage: tervi pair --server <url>` |
| `--server` given more than once | The usage line |
| Invalid server address | `Invalid server address: <value>` then the usage line |
| Unknown flag, including a single-dash `-server` | `Unknown flag: <flag>` then the usage line |
| An extra argument (`tervi pair --server x extra`) | `Unknown argument: <arg>` then the usage line |
| Unknown command, including a flag given before the command (`tervi --server x pair` → `Unknown command: --server`) | `Unknown command: <command>` then the usage line |
| No command, or `-h` / `--help` anywhere in the arguments (`tervi pair --help`, `tervi --help pair`) | The usage line only |
| `--server` as the last argument, `--server=` with an empty value, or `--server` followed by an argument starting with `-` (`--server --foo`) | The usage line |

When several of these apply:

1. `-h` or `--help` anywhere wins over everything else.
2. Otherwise the arguments are checked **left to right**, and the first error
   found is reported. For example, `tervi pair --bogus --server=banana` gives
   `Unknown flag: --bogus`.
3. A missing `--server` is reported only when no earlier error was found. For
   example, `tervi pair extra` gives `Unknown argument: extra`.

Both `--server <url>` and `--server=<url>` are accepted. Parse the arguments by
hand rather than with Go's `flag` package, which prints its own messages and
accepts single-dash flags.

### A valid server address, and its standard form

Valid: scheme `http` or `https`, a host, and an optional port. A single
trailing `/` is allowed. A path, a query (`?`), a fragment (`#`), or a user
name makes it invalid. A host containing `:` is valid only as a bracketed IPv6
address (`http://[::1]:8080`); an IPv6 zone (`%`) is invalid. A port must be a
number from 1 to 65535.

Standard form, used for saving and comparing: scheme and host in lowercase,
the port written as a plain number without leading zeros (`:08080` → `:8080`),
the default port removed (`:80` for `http`, `:443` for `https`), no trailing
`/`. So `HTTP://LOCALHOST:8080/` becomes `http://localhost:8080`.

### The secret store: backend and logic

**The backend** only gets, sets, and deletes raw text values by user name, all
under the service `tervi`:

```go
var ErrNotFound = errors.New("not found")

type Backend interface {
    Get(user string) (string, error)   // ErrNotFound when there is no value
    Set(user, value string) error
    Delete(user string) error          // deleting nothing is not an error
}
```

The real backend (GNOME Keyring through `github.com/zalando/go-keyring`) is
task 06. This task builds an **exported in-memory backend**, in a non-test
file, that every later worker test uses:

```go
func NewMemoryBackend() *MemoryBackend

type MemoryBackend struct {
    FailGet, FailSet, FailDelete bool // the operation returns an error
    ChangeOnRead bool                 // Get returns a different value than was set
    // unexported fields
}

func (m *MemoryBackend) Value(user string) (string, bool) // what is stored, for tests
```

**The store logic** sits on top of any backend:

```go
type Entry struct {
    Server     string        // standard form
    Credential secret.Value
}

func NewStore(b Backend) *Store
func (s *Store) Get() (Entry, bool, error) // user "worker"; bool: whether it exists
func (s *Store) Save(e Entry) error        // set, read back, compare
func (s *Store) Delete() error
func (s *Store) Check() error              // is the store usable?
```

- The stored value is JSON: `{"server": "http://localhost:8080", "credential": "<credential>"}`.
  Write the credential with `Reveal()` explicitly; plain `json.Marshal` of a
  `secret.Value` gives `"[hidden]"`.
- **Save** counts as successful only if reading back returns exactly what was set.
- **Check** sets a test value under user `worker-check`, reads it back,
  compares, and deletes it. Any failure means unusable.
- **A well-formed entry** is valid JSON with a `server` in standard form and a
  non-empty `credential`. `Get` reports a badly formed entry with a distinct
  error, `ErrDamaged`.

Every failed operation is shown and returns exit code `1`. An unusable store:

```text
✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.
  Make sure you are logged in to a desktop session and the keyring is unlocked.
```

A single failed read or delete names it (`✗ Can't read the secret store entry.`
or `✗ Can't delete the secret store entry.`) followed by the same hint line.

### `state.json`

Path: `<StateDir>/state.json`. Folder mode `0700`, file mode `0600`. Written by
creating a temporary file in the same folder, writing, syncing, and renaming it
over the old one, so a crash never leaves half a file.

```json
{ "machine_id": "…", "credential_saved": false, "confirmed": false }
```

It holds nothing secret and no server address.

- A file that exists but cannot be read, or is not valid JSON: `✗ Can't read <path>.`,
  change nothing, exit `1`.
- A write that fails: `✗ Can't write <path>.`, exit `1`.
- A delete that fails: `✗ Can't delete <path>.` then
  `  Check that you can change files in <folder>, then run the same command again.`,
  exit `1`. Deleting a file that does not exist is not an error.

Export the read and write functions, plus `StatePath(dir string) string` and
`DeleteState(dir string) error`; task 07 uses them.

### Step 0 — deciding what to do

**Check the last row first:** an entry that is not well formed always gives
message C, whatever `state.json` holds. Then check the other rows from the top.

| `state.json` | Entry | Outcome |
| --- | --- | --- |
| Missing | Missing | `Check` the store. Usable → `flow.StartNew`. |
| Missing | Exists | Leftover data (message A below), then as the first row |
| `credential_saved: false` | Missing | Leftover data (message D), then as the first row |
| `credential_saved: false` or `true`, `confirmed: false` | Exists, well formed | Compare `--server` (standard form) with the entry's server **first**. Different → `✗ A pairing with <saved server> isn't finished yet.` and `To finish it, run:  tervi pair --server <saved server>`; **change nothing**; exit `1`. Equal → if `credential_saved` is `false`, set it to `true`; then `flow.FinishEarlier`. |
| `credential_saved: true` | Missing | Leftover data (message B), then as the first row |
| `confirmed: true` | Exists, well formed | `This computer is already paired with <saved server>. Nothing was changed.`; exit `1` |
| Any, or missing | Exists, **not** well formed | Leftover data (message C), then as the first row |

**Leftover data** means: delete the entry if one exists, then delete
`state.json` if it exists, then print the message, then continue as the first
row (`Check`, then `flow.StartNew`) in the same run. The entry is deleted
first so that, if that fails, `state.json` still records that something is
left. If a delete fails, print only its failure message (`✗ Can't delete the secret store entry.`
with its hint, or the `state.json` delete message), keep whatever was not
deleted, and exit `1`; the next run tries again.

```text
A: Found incomplete pairing data: the secret store entry exists, but state.json is missing.
B: Found incomplete pairing data: state.json exists, but the secret store entry is missing.
C: Found a damaged secret store entry for tervi.
D: The earlier pairing was interrupted before the credential was saved.
```

A, B, C, and D are each followed by:

```text
  Removed the leftover pairing data. Starting a new pairing.
```

A deleted entry may have belonged to a machine the server still lists as
`active`. That machine can never connect again; removing it from the server
comes with the Machines page in a later slice.

### Where the code goes

`internal/worker/local/`.

## Boundaries

- May create or change: `internal/worker/local/`.
- Must not change: everything else, except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add dependencies. No test uses the real keyring.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUsageErrors` — each input in the command table: exact message on standard error, exit `2`, nothing read or written; `--server=<url>` and `--server <url>` both accepted; also `tervi pair --help`, `tervi --help pair`, `tervi pair --server`, `tervi pair --server=`, `tervi pair --server --foo`, `tervi pair --bogus --server=banana` (→ `Unknown flag: --bogus`), and `tervi pair extra` (→ `Unknown argument: extra`) | R1 |
| `TestAddressValid` — accepts `http://localhost:8080`, `https://example.com`, `http://localhost:8080/`; rejects `banana`, `ftp://x`, `http://`, `http://localhost:8080/foo`, `http://a?b`, `http://a#b`, `http://u@a`, `http://a:8080:9090`, `http://[fe80::1%25eth0]:80`, `http://a:0`, `http://a:70000`; accepts `http://[::1]:8080` | R1 |
| `TestAddressStandardForm` — `HTTP://LOCALHOST:8080/` → `http://localhost:8080`; `https://example.com:443` → `https://example.com`; `http://example.com:80` → `http://example.com`; `http://example.com:08080` → `http://example.com:8080`; `http://[::1]:8080` stays as it is | R30 |
| `TestStoreCheck` — a working backend passes and leaves no `worker-check` value; `FailGet`, `FailSet`, `FailDelete`, or `ChangeOnRead` make it unusable | R3 |
| `TestStoreSaveVerified` — with `ChangeOnRead`, `Save` fails | R18 |
| `TestEntryJSONHoldsRealCredential` — the backend's stored value contains the revealed credential, never `[hidden]` | R18 |
| `TestEntryWellFormed` — invalid JSON, a non-standard `server`, or an empty `credential` give `ErrDamaged` | R31 |
| `TestStateFileWrite` — mode `0600`, folder `0700`, all three fields round-trip, never a partial file | Safe local writes |
| `TestStateFileBroken` — invalid JSON: the read message, nothing changed, exit `1` | Defined failure |
| `TestStepZero` — one case per row of the step 0 table: the right outcome or flow call, exact message on standard output, exit code, and only the files and values that row says are changed | R2, R29, R30, R31 |
| `TestStepZeroOtherServerChangesNothing` — `credential_saved: false` and `true`, each with a different `--server`: the refusal, `state.json` byte-for-byte unchanged, `FinishEarlier` never called | R30 |
| `TestUnusableStoreStopsBeforeFlow` — `Check` fails: the message, exit `1`, `StartNew` never called | R3 |
| `TestStateWriteFailsInStepZero` — the `credential_saved` update cannot be written (read-only `StateDir`): the write message, exit `1`, `FinishEarlier` never called | Defined failure |
| `TestCtrlCDuringStepZero` — `ctx` cancelled before and during step 0: `Pairing cancelled. Nothing was saved.`, exit `130`, no flow call; cancelled after a well-formed unconfirmed entry for the same server was found: the not-confirmed message with the finish command, exit `130` | R23 |
| `TestDamagedEntryWins` — a damaged entry with `state.json` missing, and with each `state.json` value: always message C, both removed, then `StartNew` | R31 |
| `TestLeftoverThenStartNew` — rows A, B, and D: the message, the leftover removed, then `Check` and `StartNew` in the same run | R31 |
| `TestLeftoverDeleteFails` — `FailDelete` with an entry left over: the entry delete message, exit `1`, `state.json` kept, `StartNew` never called; `state.json` cannot be deleted (read-only `StateDir`): the `state.json` delete message, exit `1`, `StartNew` never called | R31 |
| `TestStateFileDelete` — `DeleteState` removes the file; a missing file is not an error | Defined failure |
| `TestCredentialNeverPrinted` — across all tests, nothing written to standard output or error contains the credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

The real keyring backend and `cmd/tervi` (task 06). Talking to the server,
saving during pairing, the acknowledgment, and Ctrl+C after step 0 (tasks 06,
07). The server (01–04).

## If anything is unclear

Stop and report the question. Do not guess.
