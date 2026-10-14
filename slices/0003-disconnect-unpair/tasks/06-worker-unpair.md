# Task 06 — Worker: `tervi unpair`

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 02, 05

## Goal

Add `tervi unpair`. After the user confirms, it tells the server (once),
then removes the pairing on the computer in a fixed order: stops and
disables the background service, makes sure no daemon is left, removes the
service file, deletes `credential.json`, then `state.json`. `--local-only`
skips the server. Every failure says what is left and how to finish, and
running the command again is always safe.

## Requirements (copied from the spec)

- R10. When the user runs `tervi unpair` on a paired computer, the worker
  shall print
  `This removes this computer's pairing with <server>.` then
  `tervi will stop running in the background. Continue? [y/N]` and wait.
  Only `y` or `yes` continues, after removing spaces around the answer and
  ignoring case; anything else prints `Nothing was changed.` and exits with
  `1`. `--yes` skips the question.
- R11. When the user confirms, the worker shall first tell the server, which
  shall mark the machine unpaired and refuse its credential from then on.
  Then the worker shall stop tervi in the background, stop it from starting
  at boot, remove its service file, delete `credential.json`, and delete
  `state.json`, print
  `✓ Unpaired. This computer is no longer paired with <server>.`, and exit
  with `0`. The `tervi` program shall stay installed. Linger shall stay as
  it is; if it is on, the worker shall then print
  `  Linger is still on. Turn it off with: loginctl disable-linger`.
- R12. If no answer comes from the server, then `tervi unpair` shall remove
  nothing on the computer, print
  `✗ No answer from <server>.` then
  `  The server may or may not have unpaired this computer. Nothing was removed here.`
  then `  Run tervi unpair again to finish, or revoke this computer on the Machines page.`,
  and exit with `1`. If the server answers with an error instead, then
  `tervi unpair` shall remove nothing on the computer, print
  `✗ <server> couldn't unpair this computer (HTTP <code>). Nothing was removed here.`
  then `  Run tervi unpair again to finish, or revoke this computer on the Machines page.`,
  and exit with `1`. Running `tervi unpair` again shall finish the unpairing,
  whether or not the server received the first request.
- R13. If the server already refuses this computer's credential (rejected or
  revoked), then `tervi unpair` shall treat the server part as done and
  remove the pairing on the computer as in R11.
- R14. When the user runs `tervi unpair --local-only`, the worker shall print
  `This removes this computer's pairing without telling <server>.` then
  `If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]`,
  and on confirmation (answered as in R10) remove the pairing on the
  computer as in R11, without
  contacting the server, print `✓ Unpaired on this computer only.`, and exit
  with `0`. `--yes` skips the question.
- R15. When `tervi unpair` runs on a computer that is not paired, the worker
  shall print `This computer isn't paired. Nothing was changed.` and exit
  with `1`. When some of tervi's files are left but `credential.json` is
  gone (after an earlier failure), damaged, or readable by others,
  `tervi unpair` (with or without `--local-only`) shall not contact the
  server, print
  `This removes what is left of an earlier pairing on this computer. Continue? [y/N]`,
  and on confirmation (answered as in R10) remove what is left as in R11,
  print `✓ Removed what was left of the earlier pairing.`, and exit with
  `0`. `--yes` skips the question. When `credential.json` is usable but the
  pairing never finished, `tervi unpair` shall work as on a paired
  computer.
- R16. If removing something on the computer fails after the server was told
  (for example, `credential.json` cannot be deleted), then the worker shall
  say what could not be removed and print `Finish with: tervi unpair --local-only`,
  and exit with `1`.

## Context

### What exists (tasks 02 and 05, slice 2)

- The server's `POST /api/v1/machines/current/unpair` with
  `Authorization: Bearer <credential>` answers `204` (revoked now or
  earlier) or `401` (`unknown_credential`); see `api/openapi.yaml`
  (`unpairMachine`).
- `control.WithLock(runtimeDir, change)` takes `daemon.lock` without
  waiting, or returns `ErrAlreadyRunning` (task 05).
- `internal/worker/service` runs `systemctl` and `loginctl` through its
  `Runner` (tests use a fake), and knows the service file's path.
- `credential.Paired`, `credential.Store.Delete`, `state.DeleteState`
  (a missing file is not an error).
- `internal/worker/pair` builds its HTTP client in `newFlow` (a cloned
  default transport with keep-alives off, and `CheckRedirect` answering
  `http.ErrUseLastResponse`, so the credential never follows a redirect).
  Move that construction into an exported `pair.NewHTTPClient()` in
  `internal/worker/pair/client.go`, used by `newFlow` and by
  `internal/worker/unpair`, so the rules exist once.
- `internal/worker/credential/file.go` names the leftover files
  (`credential-check.json`, and `.credential-*` from `os.CreateTemp`). Add
  `credential.DeleteLeftovers(dir string) error` there, which deletes those
  two kinds of file and treats missing ones as done, so the names are not
  written twice.

### Removing the service: `internal/worker/service/remove.go` (new)

```go
// Stop stops the service and keeps it from starting at boot.
// A service that is already gone counts as stopped.
func (service *Service) Stop(ctx context.Context) error

// RemoveFile removes the service file and reloads systemd.
// A file that is already gone counts as removed.
func (service *Service) RemoveFile(ctx context.Context) error

// LingerOn reports whether linger is on for the calling user.
func LingerOn(ctx context.Context, runner Runner) (bool, error)
```

`Stop` runs `systemctl --user disable --now tervi` when the service file
exists. When it does not (for example, deleted by hand while systemd still
runs the unit), it runs `systemctl --user stop tervi` instead, where
"not loaded" (exit status `5`) counts as stopped. Read the exit status with
`errors.As(err, &coded)`, where `coded` is an
`interface{ ExitCode() int }`: `*exec.ExitError` satisfies it, and the test
fake returns an error type of its own that does. Never match the output
text. `RemoveFile` removes
`~/.config/systemd/user/tervi.service`, then always runs
`systemctl --user daemon-reload`. Any other failure of a `systemctl`
command is a real failure (R16). `LingerOn` runs
`loginctl show-user --property=Linger --value` and reads `yes` or `no`.

### Unpairing: `internal/worker/unpair/unpair.go` (new package)

Holds the steps, so `cli/unpair.go` only parses, asks, and prints. Like
`internal/worker/pair`, it implements an interface the `cli` package
defines, in `cli/unpair.go`:

```go
// Unpairer does the work of tervi unpair; internal/worker/unpair
// implements it.
type Unpairer interface {
    // TellServer sends the unpair request once (step 1).
    TellServer(ctx context.Context, entry credential.Entry) UnpairAnswer
    // RemoveLocal does steps 2 and 3. A nil result means everything is gone.
    RemoveLocal(ctx context.Context, env Env) *RemoveFailure
    // LingerOn does step 4 with service.LingerOn(ctx, env.Runner), so
    // tests and the e2e fake runner control the answer; false when it
    // cannot tell.
    LingerOn(ctx context.Context, env Env) bool
}

// UnpairAnswer is the server's answer to the unpair request.
type UnpairAnswer struct {
    Done     bool // 204, or any 401: the server part is finished
    Answered bool // false when no answer came
    Status   int  // the HTTP status when Answered and not Done
}

// RemoveFailure says what could not be removed.
type RemoveFailure struct {
    Background bool   // Service.Stop, the lock, or daemon-reload failed
    Path       string // a file that could not be deleted: the service file,
                      // credential.json, a leftover, or state.json
    Reason     string // without the credential
}
```

`internal/worker/unpair` exports `New(opts Options) cli.Unpairer`, with
`Options{RequestTimeout, LockWait time.Duration}` (zero means 10 seconds
each), so tests can use short ones. `cli.Env` gets a field
`Unpairer cli.Unpairer`, and `cmd/tervi` sets it to `unpair.New(...)`. Do not add a parameter to
`cli.Run`: `test/e2e` calls it, and builds `Env` with named fields, so a new
field does not break it.

0. **The runtime folder**, before anything else: `requireRuntimeFolder`, as
   `tervi status` does. Without `XDG_RUNTIME_DIR` it prints that line,
   exits `1`, and changes nothing, so the server is never told while the
   lock in step 2 cannot be taken.
1. **The server** (not with `--local-only`): one
   `POST <server>/api/v1/machines/current/unpair`, at most 10 seconds,
   never retried, never following a redirect. The 10 seconds are
   `Options.RequestTimeout` (above).
   - `204`, or `401` with any body: the server part is done (R11, R13).
   - No answer (cannot connect, timeout): stop; nothing is removed (R12).
   - Any other answer: stop; nothing is removed (below).
2. **The background**: `Service.Stop`, then take `daemon.lock` with
   `WithLock`, trying every 200 ms for up to `LockWait`, so no daemon is
   left (also one started by hand with `tervi daemon`). Holding the lock,
   `Service.RemoveFile`, then step 3.
3. **The files**: delete `credential.json` (without reading it); then
   `credential.DeleteLeftovers` (user's decision: after unpairing, no copy
   of the secret may remain); then `state.json`. Delete only those names;
   leave any other file in the folder, and the folder itself.
4. **Linger**: `LingerOn`; if it says `yes`, the command prints R11's linger
   line. If it fails, print nothing about linger.

A failure in steps 2–3 stops there and reports what was not removed (R16).
Every step treats something already gone as done, so running
`tervi unpair` or `tervi unpair --local-only` again finishes the job.

### What the command finds (R15, the user's decisions)

| On the computer | What happens |
| --- | --- |
| A usable `credential.json` (private, not damaged), whether `state.json` is confirmed or the pairing never finished | Unpaired as above: R10's question (or R14's with `--local-only`), steps 1–4. A pairing that never finished has a `pending` credential, which the server answers with `401`, so step 1 counts it as done (R13) |
| No usable `credential.json` (missing, damaged, or readable by others), but something else of tervi's: `state.json`, the service file, the damaged or exposed `credential.json`, or a leftover from `credential.DeleteLeftovers`'s list | Leftovers, with or without `--local-only`: the leftovers question, no request to the server, steps 2–4 |
| None of these | Not paired (R15) |

Read `credential.json` with `Store.Get` (not `credential.Paired`, which
skips the file when `state.json` is not confirmed). `ErrDamaged` and
`ErrUnprotected` mean leftovers, and the file is deleted without being
read. Any other `Store.Get` error (for example mode `0000`, or a folder of
that name) is none of these: it is reported as slice 2 reports a store that
cannot be read (the path, and nothing removed), exit `1`. `state.json` is
only checked for existence and then deleted; it is never parsed, so a
damaged `state.json` changes nothing here.

### The command: `internal/worker/cli/unpair.go` (new)

`tervi unpair [--yes] [--local-only]`, flags in any order; anything else
prints the usage line and exits `2`. Add it to the usage line.

The question (R10, R14) is answered on standard input: spaces around the
answer are removed first, then `y` or `yes` in any case continues
(`Y`, `Yes`, `YES`); anything else, including `n`, `no`,
an empty line or the end of input, prints `Nothing was changed.` and exits
`1`. `--yes` skips it.

| Outcome | Prints | Exit |
| --- | --- | --- |
| Done, server told | `✓ Unpaired. This computer is no longer paired with <server>.` | `0` |
| Done, `--local-only` | `✓ Unpaired on this computer only.` | `0` |
| … and linger is on (both) | then `  Linger is still on. Turn it off with: loginctl disable-linger` | `0` |
| No answer from the server | `✗ No answer from <server>.` / `  The server may or may not have unpaired this computer. Nothing was removed here.` / `  Run tervi unpair again to finish, or revoke this computer on the Machines page.` | `1` |
| Another answer (for example `500`) | `✗ <server> couldn't unpair this computer (HTTP <code>). Nothing was removed here.` / `  Run tervi unpair again to finish, or revoke this computer on the Machines page.` (R12) | `1` |
| Leftovers: the question | `This removes what is left of an earlier pairing on this computer. Continue? [y/N]` | — |
| Leftovers: done | `✓ Removed what was left of the earlier pairing.` (then the linger line, if on) | `0` |
| Not paired | `This computer isn't paired. Nothing was changed.` | `1` |
| Step 2 failed | `✗ Can't stop tervi in the background: <reason>` / `  Finish with: tervi unpair --local-only` | `1` |
| Step 3 failed | `✗ Can't delete <path>: <reason>` / `  Finish with: tervi unpair --local-only` | `1` |

`<server>` is from `credential.json`; the leftovers lines name no server,
because none can be read. Hint lines are indented two spaces
(slice 2, fix 6). No output may contain the credential; reuse the
redaction used by `tervi connect`.

## Boundaries

- May create or change: `internal/worker/unpair/`,
  `internal/worker/service/remove.go`, `internal/worker/cli/` (the new
  file, the dispatch, the usage line, the interface, the `Env` field),
  `cmd/tervi/` (wiring only), `internal/worker/pair/client.go` and
  `pair.go` (only moving the client into `NewHTTPClient`),
  `internal/worker/credential/file.go` (only adding `DeleteLeftovers`),
  their tests.
- Must not change: `internal/worker/control/` and
  `internal/worker/daemon/` (use them), the rest of
  `internal/worker/pair/` and `internal/worker/credential/`,
  `internal/worker/state/` (use it),
  anything under `internal/server/`, `api/`, `design/`, `slices/` except
  this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUnpairAsks` — the two question lines; `y`, `Yes`, ` yes ` continue; `n`, empty, and end of input print `Nothing was changed.`, exit `1`, change nothing | R10 |
| `TestUnpairYesSkipsQuestion` — `--yes` asks nothing | R10, R14 |
| `TestUnpairRemovesInOrder` — with a fake server answering `204`: the request carries the credential; then stop and disable, the lock, the service file and `daemon-reload`, `credential.json`, the leftovers, `state.json`, in that order; the success line; exit `0`; the `tervi` program is untouched | R11 |
| `TestUnpairLingerLine` — the linger line when `loginctl` says `yes`, none for `no` or a failure | R11 |
| `TestUnpairNoAnswer` — no server, and a server that never answers within the (short, test-set) timeout: R12's three lines, exit `1`, every file and the service untouched, one request only; a second run against a server answering `204` removes everything | R12 |
| `TestUnpairOtherAnswer` — a `500`: its lines, exit `1`, nothing removed; a second run against a server answering `204` removes everything | R12 |
| `TestUnpairServerAlreadyRefuses` — `401` with `unknown_credential`, `credential_revoked`, or another body: removes everything as for `204` | R13 |
| `TestUnpairLocalOnly` — no request at all; the two question lines; removes everything; its success line | R14 |
| `TestUnpairNotPaired` — no files: its line, exit `1` | R15 |
| `TestUnpairUnreadableCredential` — a `credential.json` with mode `0000`: the read failure with its path, exit `1`, nothing removed, no request | R15 |
| `TestUnpairDamagedState` — a usable `credential.json` next to a damaged `state.json`: unpaired as usual, both files removed | R11 |
| `TestRemoveServiceAlreadyGone` — with no service file, `Stop` runs `stop` (not `disable --now`) and counts exit status `5` as stopped; `RemoveFile` still runs `daemon-reload`; both succeed | R11, R16 |
| `TestUnpairNoRuntimeFolder` — without `XDG_RUNTIME_DIR`: the runtime folder line, exit `1`, no request, nothing removed | R11 |
| `TestUnpairLeftovers` — a service file and `state.json` but no `credential.json`; a damaged `credential.json`; one readable by others: no request, the leftovers question and success line, everything removed, with and without `--local-only` | R15, R16 |
| `TestUnpairUnfinishedPairing` — a usable `credential.json` with `state.json` missing or not confirmed: the request is sent, `401` counts as done, everything removed, R11's success line | R13, R15 |
| `TestDeleteLeftovers` — deletes `.credential-*` and `credential-check.json`, leaves other files, and a second call is a no-op | R11 |
| `TestUnpairServiceFailure` and `TestUnpairDeleteFailure` — `systemctl` failing (also `daemon-reload`: `Can't stop tervi in the background`), a service file that cannot be deleted (`Can't delete <its path>`), and, as before, a daemon that never lets go of the lock, and a file that cannot be deleted: what is left, `Finish with: tervi unpair --local-only`, exit `1`; a second run finishes | R16 |
| `TestUnpairRemovesSecretLeftovers` — a `.credential-x1y2` file holding the credential and a `credential-check.json` are deleted after `credential.json`, also with `--local-only`; another file in the folder, and the folder, stay | R11, R14 |
| `TestUnpairNoSecret` — no output line contains the credential, and no file in `~/.config/tervi/` does afterwards | R11 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- The server's unpair route (task 02).
- The end-to-end test with the real server and systemd (task 08).

## If anything is unclear

Stop and report the question. Do not guess.
