# Task 07 — End-to-end test of the connection

Slice: 0002-connection    Risk: normal    Depends on: 01, 02, 03, 04, 05, 06, 08

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
  secret store and a temporary state folder. Reuse its helpers (output
  buffers, approving through the API, reading the code); move shared
  helpers into a `helpers_test.go` if both files need them. Its proxy is
  not used here (see below).
- Task 05's `cli.Env` hooks let a test replace systemd (`Runner`), the
  program path, the control client, and the service-file folder
  (`ServiceDir`).
- Task 04's `tervi daemon` and `internal/worker/control` run the daemon with
  a runtime folder you choose (set `XDG_RUNTIME_DIR` to a temporary folder
  for the test).

### The fake background service

Replace systemd with a fake `Runner`. When it is asked to run
`systemctl --user restart tervi` or `systemctl --user enable --now tervi`,
it starts the real daemon with `cli.Run(ctx, []string{"daemon"}, env, …)`
in a goroutine, using the test's state folder, secret store, and runtime
folder. Every other `systemctl` call succeeds and does nothing. If the fake
is asked to start while a daemon is already running, the second
`tervi daemon` exits `0` through the lock (R7), as real systemd would leave
the running one alone. The fake records each call.

The program path is a fake installed path outside `os.TempDir()`, and
`ServiceDir` is `t.TempDir()`, so the test never writes the real
`~/.config/systemd/user/tervi.service`.

### How the test runs the server

The slice-1 fixture puts a proxy in front of the server that reads whole
answers, so it cannot carry a WebSocket. For these tests, do not use the
proxy: the worker pairs with, and the daemon dials, the server's own
address.

- Build the server with
  `server.New(cfg, pool, logger, connections)`, where `connections` is a
  `connection.NewList()` the test holds, and `cfg` has a 1-second check
  interval, a 3-second timeout, and `PublicURL` set to the server's address.
- Serve it with `httptest.NewUnstartedServer`, using a listener the test
  opens on `127.0.0.1:0`, so the address is known before it starts.
- **To stop the server:** `connections.CloseAll()`, then close the
  `httptest` server. **To start it again:** open a new listener on the same
  address, and serve a new `server.New(...)` with a new list, on the same
  database. A list is never used again after `CloseAll` (it is final, task
  01), so every start needs a new one.

### Scenarios

1. **Pair and connect.** Pair through the approval API and the code, as the
   slice-1 test does. The output ends with the R10 lines.
   `GET /api/v1/machines` shows the machine `online: true`. `tervi status`
   shows `● Connected to <server>`, exit `0`. `state.json` has
   `"connect": true` and `"stop_reason": null`, with the pairing fields as
   before.
2. **The server restarts.** Stop the server as above. The daemon notices at
   once (its log has `✗ Connection lost: the server is stopping` within a
   second), and `tervi status` shows `○ Reconnecting`. Start the server
   again as above. The daemon reconnects by itself, and the list shows the
   machine Online again, with `last_seen_seconds_ago` set.
3. **Newest wins.** Open a second WebSocket with the same credential (read
   it from the in-memory secret store in the test), sending no `Origin`
   header. In this order:
   - The daemon's connection is replaced: its log shows
     `✗ Connection lost: replaced by a newer connection`, and the server
     logs the warning with the machine id.
   - About 1–2 seconds later the daemon reconnects by itself (a new
     `✓ Connected to <server>` line), which replaces the test's connection:
     the test's WebSocket receives close code `4000`.
   - The server has now logged the warning twice for that machine, and the
     machine stayed Online throughout (check the list between the steps).
   Wait for each step with a deadline of a few seconds; never wait for the
   test's own connection to close first.
4. **Rejection, then pairing again.** Set the machine's status to `failed`
   in the database, then stop and start the server as in scenario 2. The
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
| `TestPairConnectsEndToEnd` — scenario 1 | R1, R10, R16, R21, R23 |
| `TestServerRestartEndToEnd` — scenario 2 | R2, R12, R16, R23, R24 |
| `TestNewestWinsEndToEnd` — scenario 3 | R15 |
| `TestRejectionAndPairAgainEndToEnd` — scenario 4 | R16, R18, R33 |
| `TestNoSecretsEndToEnd` — runs scenarios 1 and 4 and checks every output, log, answer, and `state.json` for the credential | R25 |

The slice-1 end-to-end tests still pass. `go vet ./...` and `go test ./...`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- Real systemd and real login sessions: the user checks those by hand
  (`checklist.md`).

## If anything is unclear

Stop and report the question. Do not guess.
