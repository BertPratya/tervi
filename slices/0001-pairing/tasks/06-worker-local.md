# Task 06 — Worker: command and local state

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Everything the worker does on its own computer before talking to the server:
reading the command line, checking the server address, the secret store entry
and `state.json`, and deciding what to do from them (step 0). The network
flow itself is task 07.

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

### The command (`cmd/tervi`)

The only command is `tervi pair --server <url>`.

| Input | Output (to standard error) | Exit code |
| --- | --- | --- |
| `--server` missing | `Usage: tervi pair --server <url>` | `2` |
| Invalid server address | `Invalid server address: <value>` then the usage line | `2` |
| Unknown flag | `Unknown flag: <flag>` then the usage line | `2` |
| Unknown command, or no command | `Unknown command: <command>` then the usage line (for no command, just the usage line) | `2` |

### A valid server address, and its standard form

Valid: scheme `http` or `https`, a host, and an optional port. A single
trailing `/` is allowed. A path, a query (`?`), a fragment (`#`), or a user
name makes it invalid.

Standard form, used for saving and comparing: scheme and host in lowercase,
the default port removed (`:80` for `http`, `:443` for `https`), no trailing
`/`. So `HTTP://LOCALHOST:8080/` becomes `http://localhost:8080`, and
`https://example.com:443` becomes `https://example.com`.

### The secret store entry

One entry in the OS secret store, accessed with `github.com/zalando/go-keyring`
(new dependency): service `tervi`, user `worker`. Its value is JSON:

```json
{ "server": "http://localhost:8080", "credential": "<credential>" }
```

`server` is always in standard form. The credential is held in `secret.Value`
in memory; write it into the JSON with `Reveal()` explicitly (plain
`json.Marshal` of a `secret.Value` gives `"[hidden]"`).

Put the store behind a small Go interface (get, save, delete, check), with the
`go-keyring` implementation and an in-memory implementation for tests.

- **Save** = save, read back, compare. It counts as successful only if the
  value read back is exactly what was saved.
- **Check** (is the store usable?) = save a test value under user
  `worker-check`, read it back, compare, delete it. Any failure means unusable.

**Every failed get, save, delete, or check is shown to the user** and the
worker stops with exit code `1`. For an unusable store:

```text
✗ Can't use this computer's secret store (GNOME Keyring). Pairing needs it to keep the credential safe.
  Make sure you are logged in to a desktop session and the keyring is unlocked.
```

For a single failed operation, name it instead, for example
`✗ Can't read the secret store entry.` followed by the same hint line.

### `state.json`

Path: `os.UserConfigDir()` + `/tervi/state.json` (on Linux
`~/.config/tervi/state.json`). Folder mode `0700`, file mode `0600`. Written by
creating a temporary file in the same folder, writing it, syncing it, and
renaming it over the old one, so a crash never leaves half a file.

```json
{ "machine_id": "…", "credential_saved": false, "confirmed": false }
```

It holds nothing secret and no server address. A file that exists but cannot
be read or is not valid JSON stops the worker with
`✗ Can't read ~/.config/tervi/state.json.` (the real path), changes nothing,
and exits with `1`.

### Step 0 — deciding what to do

Read `state.json` and the secret store entry, then:

| `state.json` | Entry | Outcome |
| --- | --- | --- |
| Missing | Missing | Check the store is usable (above). If usable → **start a new pairing** (task 07). |
| Missing | Exists | Incomplete data (message below), exit `1` |
| `credential_saved: false` | Missing | Delete `state.json`; show `✗ The earlier pairing was interrupted before the credential was saved. Run the same command again to start a new one.`; exit `1` |
| `credential_saved: false` | Exists | Read the entry back to verify it; set `credential_saved: true`; continue as the next row |
| `credential_saved: true`, `confirmed: false` | Exists | `--server` (standard form) equals the entry's server → **finish the earlier pairing** (task 07). Different → show `✗ A pairing with <saved server> isn't finished yet.` and `To finish it, run:  tervi pair --server <saved server>`; change nothing; exit `1` |
| `credential_saved: true` | Missing | Incomplete data, exit `1` |
| `confirmed: true` | Exists | `This computer is already paired with <saved server>. Nothing was changed.`; exit `1` |

Incomplete data (name what is missing; the two variants):

```text
✗ The local pairing data is incomplete: state.json exists, but the secret store entry is missing.
  Nothing was changed.
  To start over, delete ~/.config/tervi/state.json and the "tervi" entry in Passwords and Keys.
```

```text
✗ The local pairing data is incomplete: the secret store entry exists, but state.json is missing.
  Nothing was changed.
  To start over, delete ~/.config/tervi/state.json and the "tervi" entry in Passwords and Keys.
```

### Until task 07 exists

For the two outcomes that need the server (start a new pairing, finish the
earlier pairing), step 0 returns the outcome to its caller. In this task,
`cmd/tervi` prints `Pairing is not available yet.` and exits with `1` for
those two outcomes; task 07 replaces that.

### Where the code goes

`internal/worker/local/` for the address, the store, `state.json`, and step 0;
`cmd/tervi/main.go` for the command line and exit codes.

## Boundaries

- May create or change: `internal/worker/local/`, `cmd/tervi/`, `go.mod`,
  `go.sum`.
- Must not change: anything under `internal/` other than
  `internal/worker/local/`, `slices/` except this task's row in
  `slices/0001-pairing/plan.md`.
- Tests must never touch the real secret store entry `tervi`/`worker`. Use the
  in-memory store; a test of the real `go-keyring` store uses its own service
  name and deletes what it saved.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUsageErrors` — missing `--server`, invalid address, unknown flag, unknown command, no command: each message exactly as above, exit `2`, nothing read or written | R1 |
| `TestAddressValid` — accepts `http://localhost:8080`, `https://example.com`, `http://localhost:8080/`; rejects `banana`, `ftp://x`, `http://`, `http://localhost:8080/foo`, `http://a?b`, `http://a#b`, `http://u@a` | R1 |
| `TestAddressStandardForm` — `HTTP://LOCALHOST:8080/` → `http://localhost:8080`; `https://example.com:443` → `https://example.com`; `http://example.com:80` → `http://example.com` | R30 |
| `TestStoreCheck` — usable store passes and leaves no `worker-check` entry; a store failing to save, read, or compare is reported as unusable | R3 |
| `TestStoreSaveVerified` — a save whose read-back differs counts as failed | R18 |
| `TestEntryJSONHoldsRealCredential` — the saved entry contains the revealed credential, not `[hidden]` | R18 |
| `TestStateFileWrite` — mode `0600`, folder `0700`, round-trips all three fields, and a write never leaves a partial file | — |
| `TestStateFileBroken` — invalid JSON stops with the message above and changes nothing | — |
| `TestStepZero` — one case per row of the step 0 table, using the in-memory store and a temporary folder: right outcome, exact message, exit code, and only the files and entries the row says are changed | R2, R29, R30, R31 |
| `TestStepZeroNeverSendsToOtherServer` — with an unfinished pairing for server A and `--server` B, the outcome is the refusal, never "finish" | R30 |
| `TestCredentialNeverPrinted` — across all step 0 cases, nothing written to standard output or standard error contains the credential | R18 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Talking to the server: starting, polling, the code, saving the credential
during pairing, the acknowledgment, Ctrl+C (all task 07). The server (01–05).

## If anything is unclear

Stop and report the question. Do not guess.
