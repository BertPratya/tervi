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
  Only `y` or `yes` continues; anything else prints `Nothing was changed.`
  and exits with `1`. `--yes` skips the question.
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
  and exit with `1`. Running `tervi unpair` again shall finish the unpairing,
  whether or not the server received the first request.
- R13. If the server already refuses this computer's credential (rejected or
  revoked), then `tervi unpair` shall treat the server part as done and
  remove the pairing on the computer as in R11.
- R14. When the user runs `tervi unpair --local-only`, the worker shall print
  `This removes this computer's pairing without telling <server>.` then
  `If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]`,
  and on confirmation remove the pairing on the computer as in R11, without
  contacting the server, print `✓ Unpaired on this computer only.`, and exit
  with `0`. `--yes` skips the question.
- R15. When `tervi unpair` runs on a computer that is not paired, the worker
  shall print `This computer isn't paired. Nothing was changed.` and exit
  with `1`.
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
- The pairing flow's HTTP client settings (10-second timeout, no
  redirects followed with the credential) are in `internal/worker/pair`;
  use the same rules.

### Removing the service: `internal/worker/service/remove.go` (new)

```go
// Remove stops and disables the service and removes its file.
// A service or file that is already gone counts as removed.
func (service *Service) Remove(ctx context.Context) error

// LingerOn reports whether linger is on for the calling user.
func LingerOn(ctx context.Context, runner Runner) (bool, error)
```

`Remove` runs `systemctl --user disable --now tervi`, removes
`~/.config/systemd/user/tervi.service`, then runs
`systemctl --user daemon-reload`. `LingerOn` runs
`loginctl show-user --property=Linger --value` and reads `yes` or `no`.

### Unpairing: `internal/worker/unpair/unpair.go` (new package)

Holds the steps, so `cli/unpair.go` only parses, asks, and prints. Like
`internal/worker/pair`, it implements an interface the `cli` package
defines, and `cmd/tervi` wires it.

1. **The server** (not with `--local-only`): one
   `POST <server>/api/v1/machines/current/unpair`, at most 10 seconds,
   never retried, never following a redirect.
   - `204`, or `401` with any body: the server part is done (R11, R13).
   - No answer (cannot connect, timeout): stop; nothing is removed (R12).
   - Any other answer: stop; nothing is removed (below).
2. **The background**: `Service.Remove`, then take `daemon.lock` with
   `WithLock`, trying every 200 ms for up to 10 seconds, so no daemon is
   left (also one started by hand with `tervi daemon`). Holding the lock,
   do step 3.
3. **The files**: delete `credential.json`; then every leftover
   temporary file whose name starts with `.credential-` and
   `credential-check.json` in the same folder (user's decision: after
   unpairing, no copy of the secret may remain); then `state.json`. Delete
   only those names; leave any other file in the folder, and the folder
   itself.
4. **Linger**: `LingerOn`; if it says `yes`, the command prints R11's linger
   line. If it fails, print nothing about linger.

A failure in steps 2–3 stops there and reports what was not removed (R16).
Every step treats something already gone as done, so running
`tervi unpair` or `tervi unpair --local-only` again finishes the job.

### What counts as paired

- `credential.json` readable and `state.json` confirmed: paired.
- Neither file, and no service file: not paired (R15).
- Anything in between (left by an earlier failure, for example a service
  file and `state.json` but no `credential.json`): there is no credential
  to send, so both forms remove what is left as `--local-only` does, after
  the `--local-only` question. (This case is not in the spec yet; the
  architect has asked the user. If the user decides otherwise, this file is
  updated before the task starts.)
- A `credential.json` that is not private or is damaged: reported as
  `tervi connect` reports it in slice 2, and nothing is removed.

### The command: `internal/worker/cli/unpair.go` (new)

`tervi unpair [--yes] [--local-only]`, flags in any order; anything else
prints the usage line and exits `2`. Add it to the usage line.

The question (R10, R14) is answered on standard input: `y` or `yes`,
ignoring case and surrounding spaces, continues; anything else, including
an empty line or the end of input, prints `Nothing was changed.` and exits
`1`. `--yes` skips it.

| Outcome | Prints | Exit |
| --- | --- | --- |
| Done, server told | `✓ Unpaired. This computer is no longer paired with <server>.` | `0` |
| Done, `--local-only` | `✓ Unpaired on this computer only.` | `0` |
| … and linger is on (both) | then `  Linger is still on. Turn it off with: loginctl disable-linger` | `0` |
| No answer from the server | `✗ No answer from <server>.` / `  The server may or may not have unpaired this computer. Nothing was removed here.` / `  Run tervi unpair again to finish, or revoke this computer on the Machines page.` | `1` |
| Another answer (for example `500`) | `✗ <server> couldn't unpair this computer (HTTP <code>). Nothing was removed here.` / `  Run tervi unpair again to finish, or revoke this computer on the Machines page.` (asked the user, as above) | `1` |
| Not paired | `This computer isn't paired. Nothing was changed.` | `1` |
| Step 2 failed | `✗ Can't stop tervi in the background: <reason>` / `  Finish with: tervi unpair --local-only` | `1` |
| Step 3 failed | `✗ Can't delete <path>: <reason>` / `  Finish with: tervi unpair --local-only` | `1` |

`<server>` is from `credential.json`. Hint lines are indented two spaces
(slice 2, fix 6). No output may contain the credential; reuse the
redaction used by `tervi connect`.

## Boundaries

- May create or change: `internal/worker/unpair/`,
  `internal/worker/service/remove.go`, `internal/worker/cli/` (the new
  file, the dispatch, the usage line, the interface), `cmd/tervi/` (wiring
  only), their tests.
- Must not change: `internal/worker/control/` and
  `internal/worker/daemon/` (use them), `internal/worker/pair/`,
  `internal/worker/state/`, `internal/worker/credential/` (use them),
  anything under `internal/server/`, `api/`, `design/`, `slices/` except
  this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUnpairAsks` — the two question lines; `y`, `Yes`, ` yes ` continue; `n`, empty, and end of input print `Nothing was changed.`, exit `1`, change nothing | R10 |
| `TestUnpairYesSkipsQuestion` — `--yes` asks nothing | R10, R14 |
| `TestUnpairRemovesInOrder` — with a fake server answering `204`: the request carries the credential; then disable, the lock, the service file, `credential.json`, `state.json`, in that order; the success line; exit `0`; the `tervi` program is untouched | R11 |
| `TestUnpairLingerLine` — the linger line when `loginctl` says `yes`, none for `no` or a failure | R11 |
| `TestUnpairNoAnswer` — no server, and a server that never answers within 10 seconds: R12's three lines, exit `1`, every file and the service untouched, one request only | R12 |
| `TestUnpairOtherAnswer` — a `500`: its lines, exit `1`, nothing removed | R12 |
| `TestUnpairServerAlreadyRefuses` — `401` with `unknown_credential`, `credential_revoked`, or another body: removes everything as for `204` | R13 |
| `TestUnpairLocalOnly` — no request at all; the two question lines; removes everything; its success line | R14 |
| `TestUnpairNotPaired` — no files: its line, exit `1` | R15 |
| `TestUnpairLeftovers` — a service file and `state.json` but no `credential.json`: removed as `--local-only` | R15, R16 |
| `TestUnpairServiceFailure` and `TestUnpairDeleteFailure` — `systemctl` failing, a daemon that never lets go of the lock, and a file that cannot be deleted: what is left, `Finish with: tervi unpair --local-only`, exit `1`; a second run finishes | R16 |
| `TestUnpairRemovesSecretLeftovers` — a `.credential-x1y2` file holding the credential and a `credential-check.json` are deleted after `credential.json`, also with `--local-only`; another file in the folder, and the folder, stay | R11, R14 |
| `TestUnpairNoSecret` — no output line contains the credential, and no file in `~/.config/tervi/` does afterwards | R11 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- The server's unpair route (task 02).
- The end-to-end test with the real server and systemd (task 08).

## If anything is unclear

Stop and report the question. Do not guess.
