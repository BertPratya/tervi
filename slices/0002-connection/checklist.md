# 0002 Connection — checklist

Spec: [spec.md](spec.md)
Tried on: <date> · Commit: <short commit id>

## Before you start

Do everything on the desktop itself, logged in: the secret store and the
systemd user service both need your desktop session.

- **Terminal 1** runs the server.
- **Terminal 2** runs `tervi` commands.
- **The browser** shows `http://localhost:8080/machines`.

First, in terminal 2, from the project folder:

```bash
docker compose up -d
go run ./cmd/server migrate      # should say: Database is up to date (version 2).
go install ./cmd/tervi
which tervi                      # should print a path ending in /go/bin/tervi
cat ~/.config/tervi/state.json   # should show "confirmed": true
```

If `which tervi` prints nothing, add `$(go env GOPATH)/bin` to your `PATH`.
If `state.json` is missing or not confirmed, this computer is not paired from
slice 1: tell the architect before you go on.

Then start the server in terminal 1: `go run ./cmd/server`.

## Check by hand

Do the rows in order: each one can depend on the state the row before left.
"The page" is `http://localhost:8080/machines`; leave it open and do not
reload it unless a row says so. "Restart the server" means: in terminal 1,
press **Ctrl+C**, then run `go run ./cmd/server` again. `; echo $?` after a
command prints its exit code on the next line.

**Starting tervi in the background**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R28 | Terminal 2: `go run ./cmd/tervi connect` | `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi` | ⬜ |
| 2 | R16, R17 | `tervi status; echo $?` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect`; then `1` | ⬜ |
| 3 | R4, R5, R31 | `tervi connect` | `✓ Connected.`, and the prompt comes back | ⬜ |
| 4 | R5 | `systemctl --user is-enabled tervi` | `enabled` | ⬜ |
| 5 | R5, R6 | `cat ~/.config/systemd/user/tervi.service` | `ExecStart="<the path from which tervi>" daemon` (the path in double quotes); `Restart=on-failure`; `RestartSec=5` | ⬜ |
| 6 | R21, R23 | Open the page | `Machines`, and a row for your computer: `● <your computer>   <your OS>   Online` | ⬜ |
| 7 | R16, R17 | `tervi status; echo $?` | `● Connected to http://localhost:8080`, `Machine: <your computer>`, `Since: <time> (less than a minute)`; then `0` | ⬜ |
| 8 | R4 | `tervi connect` | `Already connected.` | ⬜ |
| 9 | R31 | Close terminal 2 | The page still shows Online | ⬜ |
| 10 | R31 | Open a new terminal 2: `tervi status` | Still `● Connected to http://localhost:8080` | ⬜ |
| 11 | R26 | `ls -la $XDG_RUNTIME_DIR/tervi` | The `.` line starts with `drwx------`, `daemon.sock` with `srw-------`; both owned by you | ⬜ |
| 12 | R27 | `ss -ltn 'sport = :8080'` | A header line, then exactly one line under it, with the local address `127.0.0.1:8080`: the server is reachable only from this computer | ⬜ |

**The server goes away and comes back**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 13 | R12 | Terminal 1: press **Ctrl+C** | The server stops | ⬜ |
| 14 | R12, R8 | Terminal 2, at once: `tervi logs` | Near the end: `<time> ✗ Connection lost: the server is stopping`, then `… Reconnecting (try 1)` and `✗ Connection refused. Next try in 2 seconds` | ⬜ |
| 15 | R9 | Wait about 30 seconds, then `tervi logs` | The "Next try in" waits grow: about 2, 4, 8, 16 seconds (each may be up to 20% off) | ⬜ |
| 16 | R16, R17 | `tervi status; echo $?` | `○ Reconnecting to http://localhost:8080`, `Try <n>, next in <s> seconds`, `Last error: connection refused`; then `1` | ⬜ |
| 17 | R2, R23 | Terminal 1: `go run ./cmd/server`. Watch the page | Within about 30 seconds, without typing anything else, the page shows Online again | ⬜ |
| 18 | R2, R8 | `tervi logs` | The last line is `<time> ✓ Connected to http://localhost:8080` | ⬜ |

**The daemon stops, crashes, or freezes**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 19 | R21, R22, R24 | `systemctl --user stop tervi`. Watch the page without reloading | Within about 5 seconds: `○ <your computer>   <your OS>   Offline · last seen less than a minute ago` | ⬜ |
| 20 | R16 | `tervi status` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect` | ⬜ |
| 21 | R4 | `tervi connect` | `✓ Connected.`; the page shows Online | ⬜ |
| 22 | R6 | `systemctl --user kill -s KILL tervi` | The prompt comes back. The page may show Offline only for a moment, or not at all: systemd restarts tervi within about 6 seconds, and the page reads every 5 seconds | ⬜ |
| 23 | R6 | Wait 10 seconds: `tervi logs` | The log ends with a new `<time> ✓ Connected to http://localhost:8080`, about 6 seconds after the kill: systemd started tervi again by itself. The page shows Online | ⬜ |
| 24 | R13 | `systemctl --user kill -s STOP tervi` (freezes it without closing anything). Watch the page | Within about 35 seconds the page shows Offline | ⬜ |
| 25 | R13, R2 | `systemctl --user kill -s CONT tervi`. Watch the page | Within about 40 seconds it shows Online again | ⬜ |

**Running it by hand, and the log**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 26 | R7, R30 | `tervi daemon` | `tervi is already running in the background.`, and the prompt comes back | ⬜ |
| 27 | R30 | `systemctl --user stop tervi` | The page shows Offline within about 5 seconds | ⬜ |
| 28 | R30 | `tervi daemon` | It keeps running in this terminal and prints `<time> ✓ Connected to http://localhost:8080`; the page shows Online | ⬜ |
| 29 | R30 | Press **Ctrl+C** | It stops and the prompt comes back; the page shows Offline | ⬜ |
| 30 | R4 | `tervi connect` | `✓ Connected.`; the page shows Online | ⬜ |
| 31 | R32 | `tervi logs` | The log lines so far, then the prompt comes back | ⬜ |
| 32 | R32 | `tervi logs -f` | The log lines so far, and it keeps running | ⬜ |
| 33 | R32 | Restart the server, and watch terminal 2 | `Connection lost`, `Reconnecting`, and `Connected` lines appear as they happen | ⬜ |
| 34 | R32 | Terminal 2: press **Ctrl+C** | It stops and the prompt comes back | ⬜ |

**Restarting the computer**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 35 | R5 | Restart the computer and log in | Your desktop, as usual; nothing from tervi to type | ⬜ |
| 36 | R5 | Start only the server: `docker compose up -d`, then `go run ./cmd/server`. Open the page | Without any `tervi` command, the page shows Online within about 30 seconds | ⬜ |

**A rejected credential, and pairing again**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 37 | R18 | `docker compose exec postgres psql -U tervi -c "UPDATE machines SET status = 'failed'"` | `UPDATE` and a number | ⬜ |
| 38 | R18 | Restart the server | It starts as usual | ⬜ |
| 39 | R18 | `tervi logs` | The log ends with `✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server http://localhost:8080`, with no tries after it | ⬜ |
| 40 | R3 | `systemctl --user is-active tervi` | `active`: the daemon keeps running | ⬜ |
| 41 | R18 | `cat ~/.config/tervi/state.json` | `"connect": false` and `"stop_reason": "credential_rejected"`; the other fields as before; no credential | ⬜ |
| 42 | R16 | `tervi status` | `✗ Not connecting: the server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080` | ⬜ |
| 43 | R20, R4 | `tervi connect; echo $?` | `✗ The server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080`; then `1` | ⬜ |
| 44 | R33 | `tervi pair --server http://localhost:8080` | First `The server no longer accepts this computer's credential. Starting a new pairing.`, then the slice-1 pairing screen with a new link | ⬜ |
| 45 | R10, R31, R1 | Open the link, click **Accept**, type the code | `✓ Paired successfully.`, `✓ tervi now runs in the background and starts when you log in.`, `✓ Connected.`, and the prompt comes back. The page shows the new pairing Online | ⬜ |
| 46 | R1 | `cat ~/.config/tervi/state.json` | `"connect": true`, `"stop_reason": null` | ⬜ |
| 47 | R25 | Look back over everything terminals 1 and 2 printed, `tervi logs`, the page, and `state.json` | No credential, no long random string anywhere | ⬜ |

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R1 | Pairing makes the daemon write `connect: true`; other fields are kept | `TestPairSetsConnectThroughDaemon`, `TestStateKeepsPairingFields`, `TestPairConnectsEndToEnd` | ⬜ |
| R2 | Retries never stop or change `connect`, including while the secret store cannot be read | `TestKeepsTryingForever`, `TestStoreUnreadableIsFailedTry`, `TestServerRestartEndToEnd` | ⬜ |
| R3 | No network while `connect` is false; it still answers | `TestIdleWhileConnectFalse`, `TestStatusOverSocket`, `TestConnectOverSocket` | ⬜ |
| R4 | Every `tervi connect` result, including still trying after 30 s, a control error, and Ctrl+C | `TestConnectCommandResults`, `TestDaemonConnectResults`, `TestConnectNotPaired`, `TestConnectStartsStoppedService`, `TestConnectControlError`, `TestConnectCtrlC` | ⬜ |
| R5 | The service file and the `systemctl` steps, and `tervi connect` from a different installed `tervi` switching to it | `TestServiceFileContents`, `TestServiceFileRewrittenWhenPathChanges`, `TestEnsureRunningSteps`, `TestPairHandsOver`, `TestConnectFromDifferentInstallPrintsConnected` | ⬜ |
| R7 | Only one daemon; the lock is released after a crash or exit | `TestSecondDaemonExits`, `TestLockReleasedWhenDaemonEnds`, `TestOldSocketFileReplaced` | ⬜ |
| R8 | The exact log lines, and none for the checks | `TestLogLines`, `TestErrorTexts`, `TestDaemonPrintsLogLines` | ⬜ |
| R9 | Each wait is within 0.8–1.2 × 1, 2, 4, 8, 16, 30 s, and resets after connecting | `TestReconnectWaits` | ⬜ |
| R11 | The still-trying lines after pairing | `TestPairStillTrying` | ⬜ |
| R12 | A close by either side is noticed at once; the server closes everything when it stops | `TestClientCloseNoticedAtOnce`, `TestServerCloseNoticedAtOnce`, `TestStopsCleanlyOnContextEnd`, `TestCloseAllOnStop`, `TestCloseAllSharesDeadlineAndForcesClose`, `TestServerRestartEndToEnd` | ⬜ |
| R13 | The worker side of the timeout, and the server side at short timings | `TestServerSilenceTimesOut`, `TestServerTimeout` | ⬜ |
| R14 | The server's settings reach the worker in `hello` | `TestHelloCarriesTiming`, `TestUsesServerTiming`, `TestConnectionTimingSettings` | ⬜ |
| R15 | Newest wins, the warning, and the replaced connection never removes the new one | `TestNewestWins`, `TestReplacedCloseKeepsNewEntry`, `TestListSafeUnderLoad`, `TestNewestWinsEndToEnd` | ⬜ |
| R16 | Every status wording, including while a try is under way, and when tervi in the background has not read the server yet | `TestStatusOutputs`, `TestStatusSince`, `TestStatusWhileTrying`, `TestStatusReconnectsWhenSecretStoreIsUnreadable` | ⬜ |
| R18 | Only `401 unknown_credential` stops the daemon | `TestUnknownCredentialRefused`, `TestRejectionStops`, `TestRejectionAndPairAgainEndToEnd` | ⬜ |
| R19 | Every other failure leads to another try | `TestOnlyUnknownCredentialStops`, `TestDatabaseFailureIsNotRejection` | ⬜ |
| R20 | One try after `tervi connect`, and stopping again | `TestConnectAfterRejection` | ⬜ |
| R21 | Only finished pairings, sorted by name; every `<time ago>` wording | `TestListOnlyActiveSorted`, `TestListLastSeen`, `machines-view.test.js` | ⬜ |
| R22 | The page waits 5 seconds between reads | `machines-view.test.js` | ⬜ |
| R23 | Online comes only from the open connections, and starts empty | `TestListOnlineFromMemory`, `TestOnlineListStartsEmpty` | ⬜ |
| R24 | When `last_seen_at` is written, including on a clean stop | `TestLastSeen`, `TestCloseAllOnStop`, `TestMigrationAddsLastSeen` | ⬜ |
| R25 | No credential in any output, log, answer, or file | `TestCredentialNeverLogged`, `TestListHasNoCredential`, `TestCredentialNeverInLogOrState`, `TestNoCredentialPrinted`, `TestNoCredentialInPairOutput`, `TestNoSecretsEndToEnd` | ⬜ |
| R26 | Another user is refused even if it could reach the socket | `TestOtherUserRefused`, `TestRuntimeFolderPrivate` | ⬜ |
| R28 | `tervi pair` checks before contacting the server | `TestInstalledCheck`, `TestPairInstalledCheckFirst` | ⬜ |
| R29 | A service that can't start keeps the pairing | `TestEnsureRunningFailures`, `TestConnectServiceFails`, `TestPairServiceFails` | ⬜ |
| R31 | The commands return while the daemon keeps running | `TestConnectReturnsAfterLastLine`, `TestPairReturnsAfterLastLine` | ⬜ |
| R33 | Pairing again only after a rejection; otherwise the slice-1 refusal | `TestPairAgainAfterRejection`, `TestAlreadyPairedStillRefused`, `TestPairAgainDeleteFails` | ⬜ |
| R34 | A `500` logs what kind of operation failed, and never the error's details, a value, or a credential | `TestInternalErrorLogsKindOnly`, `TestPairingInternalErrorLogged`, `TestDatabaseFailureIsNotRejection`, `TestListDatabaseFailure` | ⬜ |

Rows 24–25 freeze the daemon with a signal instead of cutting the network;
for the server that is the same: the connection stays open and goes silent.

Tests run: `go test ./...` on <commit> → <result>
