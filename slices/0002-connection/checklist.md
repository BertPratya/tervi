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
reload it unless a row says so.

**Starting tervi in the background**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R28 | Terminal 2: `go run ./cmd/tervi connect` | `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi` | ⬜ |
| 2 | R16 | `tervi status`, then `echo $?` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect`; then `1` | ⬜ |
| 3 | R4, R5, R31 | `tervi connect` | `✓ Connected.`, and the prompt comes back | ⬜ |
| 4 | R5 | `systemctl --user is-enabled tervi`, then `cat ~/.config/systemd/user/tervi.service` | `enabled`; the file's `ExecStart=` line is the path from `which tervi` followed by ` daemon` | ⬜ |
| 5 | R21, R23 | Open the page | `Machines`, and one row: `● <your computer>   <your OS>   Online` | ⬜ |
| 6 | R16, R17 | `tervi status`, then `echo $?` | `● Connected to http://localhost:8080`, `Machine: <your computer>`, `Since: <time> (less than a minute)`; then `0` | ⬜ |
| 7 | R4 | `tervi connect` | `Already connected.` | ⬜ |
| 8 | R31 | Close terminal 2. Open a new terminal 2 and run `tervi status` | Still `● Connected to http://localhost:8080`; the page still shows Online | ⬜ |
| 9 | R26 | `ls -la $XDG_RUNTIME_DIR/tervi` | The folder line starts with `drwx------`, `daemon.sock` with `srw-------`; both owned by you | ⬜ |

**The server goes away and comes back**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 10 | R12, R8 | Terminal 1: stop the server with **Ctrl+C**. Terminal 2: `tervi logs` | Near the end: `<time> ✗ Connection lost: the server is stopping`, then `… Reconnecting (try 1)` and `✗ Connection refused. Next try in 2 seconds` lines | ⬜ |
| 11 | R9 | Wait about 30 seconds, then `tervi logs` again | The "Next try in" waits grow: about 2, 4, 8, 16 seconds (each may be up to 20% off) | ⬜ |
| 12 | R16, R17 | `tervi status`, then `echo $?` | `○ Reconnecting to http://localhost:8080`, `Try <n>, next in <s> seconds`, `Last error: connection refused`; then `1` | ⬜ |
| 13 | R2, R23 | Terminal 1: start the server again. Watch the page, then `tervi logs` | Within about 30 seconds, without typing anything else, the page shows Online again, and the log has `✓ Connected to http://localhost:8080` | ⬜ |

**The daemon stops, crashes, or freezes**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 14 | R21, R22, R24 | `systemctl --user stop tervi`. Watch the page without reloading | Within about 5 seconds: `○ <your computer>   <your OS>   Offline · last seen less than a minute ago` | ⬜ |
| 15 | R16 | `tervi status` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect` | ⬜ |
| 16 | R4 | `tervi connect` | `✓ Connected.`; the page shows Online | ⬜ |
| 17 | R6 | `systemctl --user kill -s KILL tervi`. Wait 10 seconds, then `systemctl --user is-active tervi` | `active`; the page shows Online again | ⬜ |
| 18 | R13 | `systemctl --user kill -s STOP tervi` (freezes it without closing anything). Watch the page | Within about 35 seconds the page shows Offline | ⬜ |
| 19 | R13, R2 | `systemctl --user kill -s CONT tervi`. Watch the page | Within about 40 seconds it shows Online again | ⬜ |

**Running it by hand, and the log**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 20 | R7, R30 | `tervi daemon` | `tervi is already running in the background.`, and the prompt comes back | ⬜ |
| 21 | R30 | `systemctl --user stop tervi`, then `tervi daemon` | It keeps running in this terminal and prints `<time> ✓ Connected to http://localhost:8080`; the page shows Online | ⬜ |
| 22 | R30 | Press **Ctrl+C**. Then `tervi connect` | It stops and the prompt comes back; the page goes Offline. Then `✓ Connected.` and Online | ⬜ |
| 23 | R32 | `tervi logs` | The log lines so far, then the prompt comes back | ⬜ |
| 24 | R32 | `tervi logs -f`. In terminal 1 restart the server (Ctrl+C, then start it). Then Ctrl+C in terminal 2 | New lines (`Connection lost`, `Reconnecting`, `Connected`) appear as they happen; Ctrl+C stops it | ⬜ |

**Restarting the computer**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 25 | R5 | Restart the computer and log in. Start only the server (`docker compose up -d`, then `go run ./cmd/server`). Open the page | Without any `tervi` command, the page shows Online within about 30 seconds | ⬜ |

**A rejected credential, and pairing again**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 26 | R18, R3 | `docker compose exec postgres psql -U tervi -c "UPDATE machines SET status = 'failed'"`. Restart the server (Ctrl+C, then start it). Then `tervi logs` and `systemctl --user is-active tervi` | The log ends with `✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server http://localhost:8080`, and no more tries after it; `active` (the daemon keeps running) | ⬜ |
| 27 | R18 | `cat ~/.config/tervi/state.json` | `"connect": false` and `"stop_reason": "credential_rejected"`; the other fields as before; no credential | ⬜ |
| 28 | R16 | `tervi status` | `✗ Not connecting: the server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080` | ⬜ |
| 29 | R20, R4 | `tervi connect`, then `echo $?` | `✗ The server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080`; then `1` | ⬜ |
| 30 | R33 | `tervi pair --server http://localhost:8080` | First `The server no longer accepts this computer's credential. Starting a new pairing.`, then the slice-1 pairing screen with a new link | ⬜ |
| 31 | R10, R31, R1 | Open the link, click **Accept**, type the code | `✓ Paired successfully.`, `✓ tervi now runs in the background and starts when you log in.`, `✓ Connected.`, and the prompt comes back. The page shows the new pairing Online | ⬜ |
| 32 | R1 | `cat ~/.config/tervi/state.json` | `"connect": true`, `"stop_reason": null` | ⬜ |
| 33 | R25 | Look back over everything terminals 1 and 2 printed, `tervi logs`, the page, and `state.json` | No credential, no long random string anywhere | ⬜ |

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R1 | Pairing makes the daemon write `connect: true`; other fields are kept | `TestPairSetsConnectThroughDaemon`, `TestStateKeepsPairingFields` | ⬜ |
| R2 | Retries never stop or change `connect` | `TestKeepsTryingForever`, `TestServerRestartEndToEnd` | ⬜ |
| R3 | No network while `connect` is false; it still answers | `TestIdleWhileConnectFalse`, `TestStatusOverSocket`, `TestConnectOverSocket` | ⬜ |
| R4 | Every `tervi connect` result, including still trying after 30 s | `TestConnectResults`, `TestConnectNotPaired`, `TestConnectStartsStoppedService` | ⬜ |
| R5 | The service file and the `systemctl` steps | `TestServiceFileContents`, `TestServiceFileRewrittenWhenPathChanges`, `TestEnsureRunningSteps`, `TestPairHandsOver` | ⬜ |
| R7 | Only one daemon; the lock is released after a crash or exit | `TestSecondDaemonExits`, `TestLockReleasedWhenDaemonEnds`, `TestOldSocketFileReplaced` | ⬜ |
| R8 | The exact log lines, and none for the checks | `TestLogLines`, `TestDaemonPrintsLogLines` | ⬜ |
| R9 | Each wait is within 0.8–1.2 × 1, 2, 4, 8, 16, 30 s, and resets after connecting | `TestReconnectWaits` | ⬜ |
| R11 | The still-trying lines after pairing | `TestPairStillTrying` | ⬜ |
| R12 | A close by either side is noticed at once; the server closes everything when it stops | `TestClientCloseNoticedAtOnce`, `TestServerCloseNoticedAtOnce`, `TestCloseAllOnStop`, `TestServerRestartEndToEnd` | ⬜ |
| R13 | The worker side of the timeout, and the server side at short timings | `TestServerSilenceTimesOut`, `TestServerTimeout` | ⬜ |
| R14 | The server's settings reach the worker in `hello` | `TestHelloCarriesTiming`, `TestUsesServerTiming`, `TestConnectionTimingSettings` | ⬜ |
| R15 | Newest wins, the warning, and the replaced connection never removes the new one | `TestNewestWins`, `TestReplacedCloseKeepsNewEntry`, `TestListSafeUnderLoad`, `TestNewestWinsEndToEnd` | ⬜ |
| R18 | Only `401 unknown_credential` stops the daemon | `TestUnknownCredentialRefused`, `TestRejectionStops`, `TestRejectionAndPairAgainEndToEnd` | ⬜ |
| R19 | Every other failure leads to another try | `TestOnlyUnknownCredentialStops`, `TestDatabaseFailureIsNotRejection` | ⬜ |
| R20 | One try after `tervi connect`, and stopping again | `TestConnectAfterRejection` | ⬜ |
| R21 | Only finished pairings, sorted by name; every `<time ago>` wording | `TestListOnlyActiveSorted`, `TestListLastSeen`, `machines-view.test.js` | ⬜ |
| R23 | Online comes only from the open connections, and starts empty | `TestListOnlineFromMemory`, `TestOnlineListStartsEmpty` | ⬜ |
| R24 | When `last_seen_at` is written | `TestLastSeen`, `TestMigrationAddsLastSeen` | ⬜ |
| R25 | No credential in any output, log, answer, or file | `TestCredentialNeverLogged`, `TestListHasNoCredential`, `TestCredentialNeverInLogOrState`, `TestNoCredentialPrinted`, `TestNoCredentialInPairOutput`, `TestNoSecretsEndToEnd` | ⬜ |
| R26 | Another user is refused even if it could reach the socket | `TestOtherUserRefused`, `TestRuntimeFolderPrivate` | ⬜ |
| R28 | `tervi pair` checks before contacting the server | `TestInstalledCheck`, `TestPairInstalledCheckFirst` | ⬜ |
| R29 | A service that can't start keeps the pairing | `TestEnsureRunningFailures`, `TestConnectServiceFails`, `TestPairServiceFails` | ⬜ |
| R31 | The commands return while the daemon keeps running | `TestConnectReturnsAfterLastLine`, `TestPairReturnsAfterLastLine` | ⬜ |
| R33 | Pairing again only after a rejection; otherwise the slice-1 refusal | `TestPairAgainAfterRejection`, `TestAlreadyPairedStillRefused`, `TestPairAgainDeleteFails` | ⬜ |

Rows 18–19 freeze the daemon with a signal instead of cutting the network;
that is the same for the server: the connection stays open and goes silent.
R27 is a known limitation, not a check.

Tests run: `go test ./...` on <commit> → <result>
