# Task 08 — End-to-end test of disconnect, unpair, Revoke, and pairing again

Slice: 0003-disconnect-unpair    Risk: normal    Depends on: 01–07

## Goal

Prove the slice with the real server, the real database, and the real
`tervi` commands working together, using the end-to-end fixture from slice
2 (`test/e2e`): disconnecting and connecting again, unpairing, revoking
from the Machines page, and pairing the same computer again. No secret
appears anywhere.

## Requirements (copied from the spec)

- R1. When the user runs `tervi disconnect` on a paired computer that is
  connected or reconnecting, the worker shall close the connection, stop
  trying to connect, keep the pairing, and print
  `✓ Disconnected. tervi stays in the background but won't connect.` then
  `Connect again with: tervi connect`, and exit with `0`. The daemon shall
  keep running, and write the log line `○ Disconnected.`
- R2. While the user has disconnected, the daemon shall not connect, also
  after the computer restarts, until the user runs `tervi connect`.
- R7. While the user has disconnected, `tervi status` shall show
  `○ Disconnected.` then `  Connect with: tervi connect`, and exit with `1`,
  also when tervi is not running in the background.
- R8. While the user has disconnected, the Machines page shall show the
  computer as `Offline · last seen <time ago>`, as for any computer that is
  not connected.
- R9. `tervi connect` after a disconnect shall connect again, as in slice 2's
  R4.
- R11. When the user confirms, the worker shall first tell the server, which
  shall mark the machine unpaired and refuse its credential from then on.
  Then the worker shall stop tervi in the background, stop it from starting
  at boot, remove its service file, delete `credential.json`, and delete
  `state.json`, print
  `✓ Unpaired. This computer is no longer paired with <server>.`, and exit
  with `0`. The `tervi` program shall stay installed. Linger shall stay as
  it is; if it is on, the worker shall then print
  `  Linger is still on. Turn it off with: loginctl disable-linger`.
- R18. When the user confirms, the server shall mark the machine revoked,
  refuse its credential from then on, close its connection if it is
  connected, and stop listing it. The page shall stop showing it at once.
  If the request fails, the row shall stay and the page shall show
  `Couldn't revoke <name>. Try again.`
- R20. When the server refuses this computer's credential because it was
  revoked, the daemon shall stop trying, as in slice 2's R18, and write the
  log line
  `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server <server>`.
- R21. On such a computer, `tervi status`, `tervi connect`, and
  `tervi disconnect` shall print `✗ This computer was unpaired on the server.`
  then `Pair it again with: tervi pair --server <server>`. `tervi status` and
  `tervi connect` shall exit with `1`; `tervi disconnect` with `0`, as in R5.
- R26. When the user clicks **Accept** on such a page and the pairing
  completes, the server shall continue the earlier machine instead of
  creating a new one: the same entry, keeping its history, now with the
  computer's current name. If the earlier machine's credential still worked,
  it shall stop working.

## Context

### What exists

`test/e2e/connection_helpers_test.go` has `newConnectedFixture` (a real
server on a test database, the worker's commands run in-process, and a
fake `systemctl`/`loginctl` runner that starts real daemons),
`pairLiveMachine`, `f.status`, `f.machine`, `waitForMachineOnline`,
`waitForControlState`, `f.audit`, `assertNoConnectionSecret`, and
`assertCredentialOnlyInPrivateFile`. Extend the fixture where a test needs
it: running `tervi disconnect` and `tervi unpair --yes`, a
`/etc/machine-id` file of the fixture's own (passed through
`pair.Options.MachineIDPath`, task 07), calling the Revoke route as the page does, and answering the
approval page with Accept.

### The tests: `test/e2e/unpair_test.go` (new)

1. **Disconnect and connect.** Pair; `tervi disconnect` prints R1's lines
   and exits `0`; the machine is Offline within 5 seconds and its daemon
   still runs; `tervi status` shows `○ Disconnected.`; start a new daemon
   (as after a reboot) and check it stays Offline for 3 check intervals;
   `tervi connect` makes it Online again.
2. **Disconnect with no daemon.** Pair; stop the daemon; `tervi disconnect`
   prints R3's lines; start a daemon; it stays Offline.
3. **Unpair.** Pair; `tervi unpair --yes` exits `0`; the machine leaves the
   list; its credential is `revoked` in the database, its `machines` row
   remains; `credential.json`, `state.json`, and the service file are gone;
   the fake runner saw the service disabled; a connection with the old
   credential gets `401 credential_revoked`.
4. **Revoke while connected.** Pair; call the Revoke route for the machine;
   the daemon logs R20's line, stops trying, and `state.json` says
   `credential_revoked`; `tervi status` and `tervi connect` print the
   unpaired message and exit `1`; the machine is not listed.
5. **Revoke while offline.** Pair; stop the daemon; revoke; start a daemon:
   it gets `credential_revoked` on its first try and stops the same way.
6. **Pairing the same computer again.** Pair with a fixed machine ID;
   unpair; pair again with the same machine ID: the approval answer has
   `earlier_machine` with `still_paired: false`; Accept and the code; the
   machine ID answered is the first one; the list has exactly one entry,
   Online; after a revoke instead of an unpair, the same holds.
7. **No secrets.** Across tests 1–6: no credential, machine ID, or identity
   appears in any output, log, page, or file, except the credential in
   `credential.json` while paired.

Use the wait helpers, never fixed sleeps, except for "stays Offline", which
waits a fixed 3 check intervals with the fixture's short interval.

## Boundaries

- May create or change: `test/e2e/` only.
- Must not change: any code outside `test/e2e/`, `api/`, `design/`,
  `slices/` except this task's row in `plan.md`. A failure that needs a code
  change is reported, not fixed here.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestDisconnectEndToEnd` — test 1 | R1, R2, R7, R8, R9 |
| `TestDisconnectWithoutDaemonEndToEnd` — test 2 | R2, R7 |
| `TestUnpairEndToEnd` — test 3 | R11 |
| `TestRevokeConnectedEndToEnd` — test 4 | R18, R20, R21 |
| `TestRevokeOfflineEndToEnd` — test 5 | R20 |
| `TestPairSameComputerAgainEndToEnd` — test 6 | R26 |
| `TestUnpairNoSecretsEndToEnd` — test 7 | R11, R26 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- Real systemd and a real reboot: the user checks those by hand
  (`checklist.md`).

## If anything is unclear

Stop and report the question. Do not guess.
