# Task 05 — Worker: command and local state

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Everything the worker does on its own computer before talking to the server:
the command line, the server address, the secret store entry, `state.json`,
and deciding what to do from them (step 0). Also an importable `Run`, so the
end-to-end test can run the real worker. The network flow is tasks 06 and 07.

## Requirements (copied from the spec)

- R1. When `tervi pair` runs without `--server`, the worker shall show
  `Usage: tervi pair --server <url>` and stop.
- R2. If this computer is already paired, then the worker shall say which
  server it is paired with, change nothing, and stop.
- R3. If the OS secret store cannot be used, then the worker shall say so and
  stop, before contacting the server.
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs.
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
- R31. If the local pairing data is incomplete (the state file without the
  secret store entry, or the entry without the state file), then the worker
  shall say what is missing, change nothing, stop, and explain how to clean up
  by hand.

## Context

### What exists (task 01)

`internal/secret`: `secret.Value` holds a secret and prints as `[hidden]`
everywhere, including `json.Marshal`. `Reveal()` returns the real string;
`FromString(s)` wraps one.

### `Run` — the importable worker

In `internal/worker/local`:

```go
type Env struct {
    Stdin          io.Reader
    Stdout, Stderr io.Writer
    Store          Store   // the secret store (below)
    StateDir       string  // the folder holding state.json
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
cancelled `ctx`; handling it is the flow's job (tasks 06, 07).

`cmd/tervi/main.go` is a thin wrapper: `ctx` from
`signal.NotifyContext(context.Background(), os.Interrupt)`, the real store,
`StateDir` = `os.UserConfigDir()` + `/tervi`, and a placeholder flow whose two
methods print `Pairing is not available yet.` and return `1`. Task 06
replaces the placeholder.

### The command line

The only command is `tervi pair --server <url>`. Usage errors go to standard
error and return exit code `2`, before anything is read or written:

| Input | Output |
| --- | --- |
| `--server` missing | `Usage: tervi pair --server <url>` |
| Invalid server address | `Invalid server address: <value>` then the usage line |
| Unknown flag | `Unknown flag: <flag>` then the usage line |
| Unknown command | `Unknown command: <command>` then the usage line |
| No command | The usage line only |

### A valid server address, and its standard form

Valid: scheme `http` or `https`, a host, and an optional port. A single
trailing `/` is allowed. A path, a query (`?`), a fragment (`#`), or a user
name makes it invalid.

Standard form, used for saving and comparing: scheme and host in lowercase,
the default port removed (`:80` for `http`, `:443` for `https`), no trailing
`/`. So `HTTP://LOCALHOST:8080/` becomes `http://localhost:8080`.

### The secret store entry

```go
type Entry struct {
    Server     string        // standard form
    Credential secret.Value
}

type Store interface {
    Get() (Entry, bool, error)   // bool: whether an entry exists
    Save(Entry) error            // save, read back, compare
    Delete() error
    Check() error                // is the store usable?
}
```

Two implementations: one with `github.com/zalando/go-keyring` (new
dependency; service `tervi`, user `worker`), and one in memory for tests that
can be told to fail any operation. **No test uses the real keyring** in this
task: there is no Secret Service in a sandbox or CI.

The stored value is JSON: `{"server": "http://localhost:8080", "credential": "<credential>"}`.
Write the credential with `Reveal()` explicitly; plain `json.Marshal` of a
`secret.Value` gives `"[hidden]"`.

- **Save** counts as successful only if reading back returns exactly what was saved.
- **Check** saves a test value under user `worker-check`, reads it back,
  compares, and deletes it. Any failure means unusable.
- **A well-formed entry** is valid JSON with a `server` in standard form and a
  non-empty `credential`. `Get` reports a badly formed entry as a distinct error.

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

It holds nothing secret and no server address. A file that exists but cannot
be read, or is not valid JSON, stops with `✗ Can't read <path>.`, changes
nothing, and returns `1`.

### Step 0 — deciding what to do

| `state.json` | Entry | Outcome |
| --- | --- | --- |
| Missing | Missing | `Check` the store. Usable → `flow.StartNew`. |
| Missing | Exists | Incomplete data (message A below), exit `1` |
| `credential_saved: false` | Missing | Delete `state.json`; `✗ The earlier pairing was interrupted before the credential was saved. Run the same command again to start a new one.`; exit `1` |
| `credential_saved: false` | Exists, well formed | Set `credential_saved: true`, then as the next row |
| `credential_saved: true`, `confirmed: false` | Exists, well formed | `--server` (standard form) equals the entry's server → `flow.FinishEarlier`. Different → `✗ A pairing with <saved server> isn't finished yet.` and `To finish it, run:  tervi pair --server <saved server>`; change nothing; exit `1` |
| `credential_saved: true` | Missing | Incomplete data (message B), exit `1` |
| `confirmed: true` | Exists, well formed | `This computer is already paired with <saved server>. Nothing was changed.`; exit `1` |
| Any | Exists, **not** well formed | Damaged entry (message C), change nothing, exit `1` |

```text
A: ✗ The local pairing data is incomplete: the secret store entry exists, but state.json is missing.
B: ✗ The local pairing data is incomplete: state.json exists, but the secret store entry is missing.
C: ✗ The secret store entry for tervi is damaged.
```

A, B, and C are each followed by:

```text
  Nothing was changed.
  To start over, delete ~/.config/tervi/state.json and the "tervi" entry in Passwords and Keys.
```

(Show the real `state.json` path.)

### Where the code goes

`internal/worker/local/` for everything above; `cmd/tervi/main.go` as the thin
wrapper.

## Boundaries

- May create or change: `internal/worker/local/`, `cmd/tervi/`, `go.mod`, `go.sum`.
- Must not change: everything else, except this task's row in
  `slices/0001-pairing/plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUsageErrors` — each input in the command table: exact message, exit `2`, nothing read or written | R1 |
| `TestAddressValid` — accepts `http://localhost:8080`, `https://example.com`, `http://localhost:8080/`; rejects `banana`, `ftp://x`, `http://`, `http://localhost:8080/foo`, `http://a?b`, `http://a#b`, `http://u@a` | R1 |
| `TestAddressStandardForm` — `HTTP://LOCALHOST:8080/` → `http://localhost:8080`; `https://example.com:443` → `https://example.com`; `http://example.com:80` → `http://example.com` | R30 |
| `TestStoreCheck` — a usable store passes and leaves no `worker-check` entry; failing save, read, or compare is reported as unusable | R3 |
| `TestStoreSaveVerified` — a save whose read-back differs counts as failed | R18 |
| `TestEntryJSONHoldsRealCredential` — the stored value contains the revealed credential, never `[hidden]` | R18 |
| `TestEntryWellFormed` — invalid JSON, a non-standard `server`, or an empty `credential` are each reported as damaged | R31 |
| `TestStateFileWrite` — mode `0600`, folder `0700`, all three fields round-trip, never a partial file | Safe local writes |
| `TestStateFileBroken` — invalid JSON: the message, nothing changed, exit `1` | Defined failure |
| `TestStepZero` — one case per row of the step 0 table: the right outcome or flow call, exact message, exit code, and only the files and entries that row says are changed | R2, R29, R30, R31 |
| `TestStepZeroNeverSendsToOtherServer` — unfinished pairing with server A, `--server` B: the refusal, `FinishEarlier` never called | R30 |
| `TestUnusableStoreStopsBeforeFlow` — `Check` fails: the message, exit `1`, `StartNew` never called | R3 |
| `TestCredentialNeverPrinted` — across all tests, nothing written to standard output or error contains the credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Talking to the server, saving the credential during pairing, the
acknowledgment, and Ctrl+C messages (tasks 06, 07). The server (01–04).

## If anything is unclear

Stop and report the question. Do not guess.
