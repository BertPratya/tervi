# Task 07 — End-to-end test of the connection

Slice: 0002-connection    Risk: normal    Depends on: 01, 02, 03, 04, 05, 06

## Goal

Run the real server and the real worker together and prove the slice works
as a whole: pairing connects, the Machines list follows the connection,
reconnecting survives a server restart, newest wins, and a rejected
credential stops the daemon and allows pairing again. systemd is replaced by
a fake that runs the real daemon in the same test process.

## Requirements (copied from the spec)

- R2. While `connect` is true, the daemon shall stay connected to the server,
  and reconnect whenever the connection is lost, with no limit on the number
  of tries.
- R10. When pairing succeeds, the terminal shall show `✓ Paired successfully.`,
  then `✓ tervi now runs in the background and starts when you log in.`, then
  `✓ Connected.` once the daemon is connected.
- R12. When either side closes the connection on purpose, the other side shall
  notice at once.
- R15. The server shall keep at most one connection per machine. When a new
  connection arrives for a machine that already has one, the server shall
  close the old one, keep the new one, and log a warning naming the machine.
- R16. `tervi status` shall show one of:
  - `● Connected to <server>`, with the machine name and since when;
  - `○ Reconnecting to <server>`, with the try number, the wait before the
    next try, and the last error;
  - `✗ tervi isn't running in the background.` with `Start it with: tervi connect`;
  - `✗ This computer isn't paired.` with `Pair it with: tervi pair --server <url>`;
  - `✗ Not connecting: the server doesn't accept this computer's credential.`
    with `Pair again with: tervi pair --server <server>`.
- R18. If the server answers that it does not know this computer's credential,
  then the daemon shall stop trying, set `connect` to false, save the reason in
  `state.json`, and print that the credential was rejected and how to pair
  again.
- R21. The server shall show `/machines`: every paired machine (only those
  whose pairing finished), sorted by name, with its name, OS, and `Online` or
  `Offline`. An
  offline machine shall also show `last seen <time ago>`, or `never connected`
  if it never connected.
- R23. A machine shall show as Online exactly while the server has its
  connection open. After the server restarts, every machine shall show as
  Offline until it connects again.
- R24. The server shall record when it last heard from each machine: when it
  connects, at least once a minute while connected, and when the connection
  ends (the time of the last message received). "Last seen" may be up to one
  minute early if the server itself stopped suddenly.
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`.
- R33. When `tervi pair` runs on a computer whose credential was rejected
  (R18), the worker shall print
  `The server no longer accepts this computer's credential. Starting a new pairing.`,
  remove the old pairing (the secret store entry and the pairing in
  `state.json`), and start a new pairing. On a paired computer whose
  credential was not rejected, `tervi pair` shall still refuse, as in slice 1
  (R2 of slice 1).

## Context

### What exists

- `test/e2e/pairing_test.go`: the slice-1 end-to-end test, with a fixture
  that runs the real server (`internal/server`) on a test database
  (`dbtest`) and the real worker command (`cli.Run`) with an in-memory
  secret store and a temporary state folder. Reuse its fixture and helpers;
  move shared helpers into a `helpers_test.go` if both files need them.
- Task 05's `cli.Env` hooks let a test replace systemd and the program path.
- Task 04's `tervi daemon` and `internal/worker/control` run the daemon with
  a runtime folder you choose (set `XDG_RUNTIME_DIR` to a temporary folder
  for the test).

### The fake background service

Replace systemd with a fake whose start step starts the real daemon
(`tervi daemon`, through `cli.Run` or the daemon and control packages) in a
goroutine, with the test's state folder, secret store, and runtime folder.
The fake records that it was asked to start. The program path is a fake
installed path outside `os.TempDir()`.

### Server timing

Run the server with a 1-second check interval and a 3-second timeout, so the
test stays fast.

### Scenarios, in one test or several

1. **Pair and connect.** Pair through the approval API and the code, as the
   slice-1 test does. The output ends with the R10 lines.
   `GET /api/v1/machines` shows the machine `online: true`. `tervi status`
   shows `● Connected to <server>`, exit `0`.
2. **The server restarts.** Stop the server (cancel its context). The daemon
   notices at once (its log has `✗ Connection lost: the server is stopping`
   within a second), and `tervi status` shows `○ Reconnecting`. Start a new
   server on the same address and database. The daemon reconnects by itself,
   and the list shows the machine Online again, with `last_seen_seconds_ago`
   set. Before it reconnects, the new server lists it Offline.
3. **Newest wins.** Open a second WebSocket with the same credential (read
   it from the in-memory secret store in the test). The daemon's connection
   is replaced (its log shows `replaced by a newer connection`), the server
   logs the warning with the machine id, and the machine stays Online. Close
   the extra connection; the daemon reconnects.
4. **Rejection, then pairing again.** Set the machine's status to `failed`
   in the database, then close its connection from the server side. The
   daemon's next try gets `401`, it stops, `state.json` has
   `connect: false` and `stop_reason: "credential_rejected"`, and
   `tervi status` shows the R16 rejection lines. `tervi pair` then prints the
   R33 line and a new pairing completes and connects.
5. **No secrets.** Across every scenario, no terminal output, daemon log,
   server log, list answer, or `state.json` contains the credential.

## Boundaries

- May create or change: `test/e2e/` only.
- Must not change: any non-test code. If a scenario fails because of a bug,
  stop and report it with the failing output.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPairConnectsEndToEnd` | R10, R16, R21, R23 |
| `TestServerRestartEndToEnd` | R2, R12, R16, R23, R24 |
| `TestNewestWinsEndToEnd` | R15 |
| `TestRejectionAndPairAgainEndToEnd` | R16, R18, R33 |
| `TestNoSecretsEndToEnd` (or a check inside each test above) | R25 |

The slice-1 end-to-end tests still pass. `go vet ./...` and `go test ./...`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- Real systemd and real login sessions: the user checks those by hand
  (`checklist.md`).

## If anything is unclear

Stop and report the question. Do not guess.
