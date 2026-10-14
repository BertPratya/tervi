# 0002 Connection — checklist

Spec: [spec.md](spec.md)
Tried on: 2026-10-14 · Commit: 7114128

The architect ran the terminal rows and checked their output; the user
clicked in the browser, watched the pages, and restarted the computer.

This checklist goes end to end: it pairs the computer as in slice 1, then
checks everything slice 2 adds. It works the same over SSH and on the
desktop itself (fix 4 moved the credential out of GNOME Keyring).

## Before you start

- **Terminal 1** runs the server.
- **Terminal 2** runs `tervi` commands.
- **The browser** opens the approval links and `http://localhost:8080/machines`.

**Over SSH:** open terminal 1 with a port forward, so the browser on your own
computer can open `http://localhost:8080`:

```bash
ssh -L 8080:localhost:8080 <this computer>
```

Terminal 2 can be a plain `ssh <this computer>`. Keep terminal 1's SSH
session open while you use the browser; it carries the forward. On the
desktop itself, any two terminals will do.

Then, in terminal 2, from the project folder:

```bash
git pull                          # the slice branch, slice/0002-connection
docker compose up -d
go run ./cmd/server migrate       # should say: Database is up to date (version 2).
go install ./cmd/tervi
which tervi                       # should print a path ending in /go/bin/tervi
systemctl --user stop tervi       # stops a daemon still running the old program
ls -la ~/.config/tervi/           # see "Earlier pairings" below
```

If `which tervi` prints nothing, `~/go/bin` is not on your `PATH`. Add it
for good, once, so every new terminal (rows 24 and 57–68 open new ones)
finds `tervi`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.bashrc && source ~/.bashrc
which tervi                       # now prints the path
```
`systemctl --user stop tervi` may say
`Failed to stop tervi.service: Unit tervi.service not loaded.` on a computer
that never ran tervi in the background; that is fine, carry on. It is needed
because installing a new `tervi` does not restart a daemon that is already
running the old one.

**Earlier pairings.** If `ls` shows a `state.json` but no `credential.json`,
this computer was paired before fix 4, with the credential in GNOME Keyring.
That is expected: the first `tervi pair` (row 3) first prints
`Found incomplete pairing data: state.json exists, but credential.json is missing.`
and `  Removed the leftover pairing data. Starting a new pairing.` (slice 1's
R31), and then what the row says. Machines from earlier pairings stay on the
Machines page as extra Offline rows until slice 0003 adds Revoke; the rows
below are about the newest one.

## Check by hand

Do the rows in order: each one can depend on the state the row before left.
"The page" is `http://localhost:8080/machines`. "Restart the server" means:
in terminal 1, press **Ctrl+C**, then run `go run ./cmd/server` again.
`; echo $?` after a command prints its exit code on the next line. Rows
marked *slice 1* repeat slice 1's checks; their requirement numbers are
slice 1's.

**Pairing (slice 1)**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R28 | Terminal 2: `go run ./cmd/tervi pair --server http://localhost:8080` | `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi`, and it stops before contacting the server | ✅ |
| 2 | slice 1 R1 | `tervi pair` (no `--server`) | The usage lines, starting with `Usage: tervi pair --server <url>`, and it stops | ✅ |
| 3 | slice 1 R4 | With the server **not** started yet: `tervi pair --server http://localhost:8080` | `✗ Can't reach http://localhost:8080. Check that the server is running, then run the command again.` | ✅ |
| 4 | slice 1 R21 | Terminal 1: `go run ./cmd/server`. Terminal 2: `curl -i -H 'Host: evil.example' http://localhost:8080/health` | The server keeps running. `HTTP/1.1 403 Forbidden` and `{"error": "unknown_host"}` | ✅ |
| 5 | slice 1 R5 | Terminal 2: `tervi pair --server http://localhost:8080` | `Pairing this computer with http://localhost:8080`, your computer name, your OS, a link, and `Waiting for approval... (expires at HH:MM)`, about 10 minutes from now | ✅ |
| 6 | slice 1 R6, R9 | Open the link in the browser, then click **Reject** | First `Pair a computer?` with the same computer name and OS, and **Accept** / **Reject**. After the click: `Rejected. This computer was not paired.` Terminal, within a few seconds: `✗ Pairing was rejected in the browser.`, and it stops | ✅ |
| 7 | slice 1 R23 | `tervi pair --server http://localhost:8080`, then press **Ctrl+C** at once | `Pairing cancelled. Nothing was saved.`, and it stops at once | ✅ |
| 8 | slice 1 R7, R12, R8 | `tervi pair --server http://localhost:8080`. Open the link, click **Accept** | Browser: `Type this code into the terminal of <your computer>:`, an 8-digit code like `4827-1934`, and `Never give this code to anyone.` Terminal: `✓ Approved in the browser.`, then `Type the code shown in the browser (expires at HH:MM, 5 tries left):`. The code itself is **not** in the terminal | ✅ |
| 9 | slice 1 R13 | Type a wrong code, for example `0000-0000` | `✗ Wrong code. 4 tries left.` | ✅ |
| 10 | slice 1 R17, R19; R10, R31 | Type the code from the browser | `✓ Code accepted.`, `✓ Credential saved.`, `Confirming with the server...`, `✓ Paired successfully.`, `✓ tervi now runs in the background and starts when the computer starts.`, `✓ Connected.`, and the prompt comes back. Browser: `✓ <your computer> is paired.` | ✅ |
| 11 | slice 1 R2 | `tervi pair --server http://localhost:8080` again | `This computer is already paired with http://localhost:8080. Nothing was changed.` | ✅ |

**The credential file**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 12 | R35 | `stat -c '%a %U %n' ~/.config/tervi ~/.config/tervi/credential.json` | `700 <you> …/.config/tervi`, then `600 <you> …/credential.json`. Do not print the file itself: it holds the credential | ✅ |
| 13 | R25, R35 | `cat ~/.config/tervi/state.json` | One line of JSON with `"machine_id":…`, `"credential_saved":true`, `"confirmed":true`, `"connect":true`, `"stop_reason":null`. No credential, no server address | ✅ |
| 14 | R5 | `loginctl show-user $USER -p Linger` | `Linger=yes` | ✅ |

**Starting tervi in the background**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 15 | R16, R17 | `systemctl --user stop tervi`, then `tervi status; echo $?` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect`; then `1` | ✅ |
| 16 | R28 | `go run ./cmd/tervi connect` | `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi` | ✅ |
| 17 | R4, R5, R31 | `tervi connect` | `✓ Connected.`, and the prompt comes back | ✅ |
| 18 | R5 | `systemctl --user is-enabled tervi` | `enabled` | ✅ |
| 19 | R5, R6 | `cat ~/.config/systemd/user/tervi.service` | `ExecStart="<the path from which tervi>" daemon` (the path in double quotes); `Restart=on-failure`; `RestartSec=5`; `WantedBy=default.target` | ✅ |
| 20 | R21, R23 | Open the page | `Machines`, and a row for your computer: `● <your computer>   <your OS>   Online` | ✅ |
| 21 | R16, R17 | `tervi status; echo $?` | `● Connected to http://localhost:8080`, `Machine: <your computer>`, `Since: <time> (less than a minute)`; then `0` | ✅ |
| 22 | R4 | `tervi connect` | `Already connected.` | ✅ |
| 23 | R31 | Close terminal 2 | The page still shows Online | ✅ |
| 24 | R31 | Open a new terminal 2: `tervi status` | Still `● Connected to http://localhost:8080` | ✅ |
| 25 | R26 | `ls -la $XDG_RUNTIME_DIR/tervi` | The `.` line starts with `drwx------`, `daemon.sock` with `srw-------`; both owned by you | ✅ |
| 26 | R27 | `ss -ltn 'sport = :8080'` | A header line, then exactly one line under it, with the local address `127.0.0.1:8080`: the server is reachable only from this computer (the SSH forward reaches it from inside) | ✅ |

**A credential file others could read**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 27 | R36 | `chmod 644 ~/.config/tervi/credential.json`, then `tervi status; echo $?` | `✗ Other users may be able to read <path>, so tervi won't use it.` and `Make it private with: chmod 600 <path>`, where `<path>` is the full path of `credential.json`; then `1` | ✅ |
| 28 | R36 | `tervi connect; echo $?` | The same two lines; then `1` | ✅ |
| 29 | R36, R2 | `systemctl --user restart tervi`, wait 5 seconds, then `tervi logs` | Near the end: `✗ Other users may be able to read credential.json. Next try in <s> seconds`. Within about 5 seconds the page shows Offline | ✅ |
| 30 | R36 | `stat -c %a ~/.config/tervi/credential.json` | `644`: tervi did not change it | ✅ |
| 31 | R36, R2 | `chmod 600 ~/.config/tervi/credential.json`. Watch the page | Within about 40 seconds, without any `tervi` command, the page shows Online again | ✅ |

**The server goes away and comes back**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 32 | R12 | Terminal 1: press **Ctrl+C** | The server stops | ✅ |
| 33 | R12, R8 | Terminal 2, at once: `tervi logs` | Near the end: `<time> ✗ Connection lost: the server is stopping`, then `… Reconnecting (try 1)` and `✗ Connection refused. Next try in 2 seconds` | ✅ |
| 34 | R9 | Wait about 30 seconds, then `tervi logs` | The "Next try in" waits grow: about 2, 4, 8, 16 seconds (each may be up to 20% off) | ✅ |
| 35 | R16, R17 | `tervi status; echo $?` | `○ Reconnecting to http://localhost:8080`, `Try <n>, next in <s> seconds`, `Last error: connection refused`; then `1` | ✅ |
| 36 | R2, R23 | Terminal 1: `go run ./cmd/server`. Watch the page | Within about 40 seconds, without typing anything else, the page shows Online again | ✅ |
| 37 | R2, R8 | `tervi logs` | The last line is `<time> ✓ Connected to http://localhost:8080` | ✅ |

**The daemon stops, crashes, or freezes**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 38 | R21, R22, R24 | `systemctl --user stop tervi`. Watch the page without reloading | Within about 5 seconds: `○ <your computer>   <your OS>   Offline · last seen less than a minute ago` | ✅ |
| 39 | R16 | `tervi status` | `✗ tervi isn't running in the background.` and `Start it with: tervi connect` | ✅ |
| 40 | R4 | `tervi connect` | `✓ Connected.`; the page shows Online | ✅ |
| 41 | R6 | `systemctl --user kill -s KILL tervi` | The prompt comes back. The page may show Offline only for a moment, or not at all: systemd restarts tervi within about 6 seconds, and the page reads every 5 seconds | ✅ |
| 42 | R6 | Wait 10 seconds: `tervi logs` | The log ends with a new `<time> ✓ Connected to http://localhost:8080`, about 6 seconds after the kill: systemd started tervi again by itself. The page shows Online | ✅ |
| 43 | R13 | `systemctl --user kill -s STOP tervi` (freezes it without closing anything). Watch the page | Within about 35 seconds the page shows Offline | ✅ |
| 44 | R13, R2 | `systemctl --user kill -s CONT tervi`. Watch the page | Within about 40 seconds it shows Online again | ✅ |

**Running it by hand, and the log**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 45 | R7, R30 | `tervi daemon` | `tervi is already running in the background.`, and the prompt comes back | ✅ |
| 46 | R30 | `systemctl --user stop tervi` | The page shows Offline within about 5 seconds | ✅ |
| 47 | R30 | `tervi daemon` | It keeps running in this terminal and prints `<time> ✓ Connected to http://localhost:8080`; the page shows Online | ✅ |
| 48 | R30 | Press **Ctrl+C** | It stops and the prompt comes back; the page shows Offline | ✅ |
| 49 | R4 | `tervi connect` | `✓ Connected.`; the page shows Online | ✅ |
| 50 | R32 | `tervi logs` | The log lines so far, then the prompt comes back | ✅ |
| 51 | R32 | `tervi logs -f` | The log lines so far, and it keeps running | ✅ |
| 52 | R32 | Restart the server, and watch terminal 2 | `Connection lost`, `Reconnecting`, and `Connected` lines appear as they happen | ✅ |
| 53 | R32 | Terminal 2: press **Ctrl+C** | It stops and the prompt comes back | ✅ |

**Restarting the computer, without logging in**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 54 | R5 | Restart the computer (`sudo reboot`). Do **not** log in on its desktop. Wait about a minute, then connect terminal 1 again with the port forward | You can log in by SSH as usual | ✅ |
| 55 | R5 | Terminal 1: `systemctl --user show tervi -p ActiveEnterTimestamp; loginctl show-session $XDG_SESSION_ID -p Timestamp` | Two times with seconds. The first (when tervi started) is **earlier** than the second (when you logged in by SSH): tervi started at boot, before anyone logged in | ✅ |
| 56 | R5 | Terminal 1, from the project folder: `docker compose up -d`, then `go run ./cmd/server` (no `tervi` command). Open the page | Within about 40 seconds, the page shows Online | ✅ |

**A rejected credential, and pairing again**

Open terminal 2 again (plain SSH) and go to the project folder for these rows.

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 57 | R18 | `id=$(grep -o '"machine_id":"[^"]*"' ~/.config/tervi/state.json \| cut -d'"' -f4)`, then `docker compose exec postgres psql -U tervi -c "UPDATE machines SET status = 'failed' WHERE machine_id = '$id'"` (only this computer's newest pairing) | `UPDATE 1` | ✅ |
| 58 | R18 | Restart the server | It starts as usual | ✅ |
| 59 | R18 | `tervi logs` | The log ends with `✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server http://localhost:8080`, with no tries after it | ✅ |
| 60 | R3 | `systemctl --user is-active tervi` | `active`: the daemon keeps running | ✅ |
| 61 | R18 | `cat ~/.config/tervi/state.json` | `"connect":false` and `"stop_reason":"credential_rejected"`; the other fields as before; no credential | ✅ |
| 62 | R16 | `tervi status` | `✗ Not connecting: the server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080` | ✅ |
| 63 | R20, R4 | `tervi connect; echo $?` | `✗ The server doesn't accept this computer's credential.` and `Pair again with: tervi pair --server http://localhost:8080`; then `1` | ✅ |
| 64 | R33 | `tervi pair --server http://localhost:8080` | First `The server no longer accepts this computer's credential. Starting a new pairing.`, then the slice-1 pairing screen with a new link | ✅ |
| 65 | R10, R31, R1 | Open the link, click **Accept**, type the code | `✓ Paired successfully.`, `✓ tervi now runs in the background and starts when the computer starts.`, `✓ Connected.`, and the prompt comes back. The page shows the new pairing Online | ✅ |
| 66 | R1 | `cat ~/.config/tervi/state.json` | `"connect":true`, `"stop_reason":null` | ✅ |
| 67 | R35 | `stat -c %a ~/.config/tervi/credential.json` | `600`: the new pairing's file is private too | ✅ |
| 68 | R25 | Look back over everything terminals 1 and 2 printed, `tervi logs`, the page, and `state.json` | No credential, no long random string anywhere. The only place it exists is `credential.json`, which you never printed | ✅ |

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R1 | Pairing makes the daemon write `connect: true`; other fields are kept | `TestPairSetsConnectThroughDaemon`, `TestStateKeepsPairingFields`, `TestPairConnectsEndToEnd` | ✅ |
| R2 | Retries never stop or change `connect`, including while `credential.json` cannot be read or is not private | `TestKeepsTryingForever`, `TestStoreUnreadableIsFailedTry`, `TestUnprotectedCredentialIsFailedTry`, `TestServerRestartEndToEnd` | ✅ |
| R3 | No network while `connect` is false; it still answers | `TestIdleWhileConnectFalse`, `TestStatusOverSocket`, `TestConnectOverSocket` | ✅ |
| R4 | Every `tervi connect` result, including still trying after 30 s, a control error, Ctrl+C, and `✓ Connected.` after starting a stopped daemon that connected first | `TestConnectCommandResults`, `TestDaemonConnectResults`, `TestConnectNotPaired`, `TestConnectStartsStoppedService`, `TestEnsureRunningReportsStoppedDaemonStarted`, `TestConnectControlError`, `TestConnectCtrlC` | ✅ |
| R5 | The service file, linger turned on before the `systemctl` steps, and `tervi connect` from a different installed `tervi` switching to it | `TestServiceFileContents`, `TestServiceFileRewrittenWhenPathChanges`, `TestEnsureRunningSteps`, `TestEnsureRunningTurnsOnLinger`, `TestPairHandsOver`, `TestConnectFromDifferentInstallPrintsConnected` | ✅ |
| R7 | Only one daemon; the lock is released after a crash or exit | `TestSecondDaemonExits`, `TestLockReleasedWhenDaemonEnds`, `TestOldSocketFileReplaced` | ✅ |
| R8 | The exact log lines, and none for the checks | `TestLogLines`, `TestErrorTexts`, `TestDaemonPrintsLogLines` | ✅ |
| R9 | Each wait is within 0.8–1.2 × 1, 2, 4, 8, 16, 30 s, and resets after connecting | `TestReconnectWaits` | ✅ |
| R11 | The still-trying lines after pairing | `TestPairStillTrying` | ✅ |
| R12 | A close by either side is noticed at once; the server closes everything when it stops | `TestClientCloseNoticedAtOnce`, `TestServerCloseNoticedAtOnce`, `TestStopsCleanlyOnContextEnd`, `TestCloseAllOnStop`, `TestCloseAllSharesDeadlineAndForcesClose`, `TestServerRestartEndToEnd` | ✅ |
| R13 | The worker side of the timeout, and the server side at short timings | `TestServerSilenceTimesOut`, `TestServerTimeout` | ✅ |
| R14 | The server's settings reach the worker in `hello` | `TestHelloCarriesTiming`, `TestUsesServerTiming`, `TestConnectionTimingSettings` | ✅ |
| R15 | Newest wins, the warning, and the replaced connection never removes the new one | `TestNewestWins`, `TestReplacedCloseKeepsNewEntry`, `TestListSafeUnderLoad`, `TestNewestWinsEndToEnd` | ✅ |
| R16 | Every status wording, including while a try is under way, and when tervi in the background has not read the server yet | `TestStatusOutputs`, `TestStatusSince`, `TestStatusWhileTrying`, `TestStatusReconnectsWhenCredentialFileIsUnreadable` | ✅ |
| R18 | Only `401 unknown_credential` stops the daemon | `TestUnknownCredentialRefused`, `TestRejectionStops`, `TestRejectionAndPairAgainEndToEnd` | ✅ |
| R19 | Every other failure leads to another try | `TestOnlyUnknownCredentialStops`, `TestDatabaseFailureIsNotRejection` | ✅ |
| R20 | One try after `tervi connect`, and stopping again | `TestConnectAfterRejection` | ✅ |
| R21 | Only finished pairings, sorted by name; every `<time ago>` wording | `TestListOnlyActiveSorted`, `TestListLastSeen`, `machines-view.test.js` | ✅ |
| R22 | The page waits 5 seconds between reads | `machines-view.test.js` | ✅ |
| R23 | Online comes only from the open connections, and starts empty | `TestListOnlineFromMemory`, `TestOnlineListStartsEmpty` | ✅ |
| R24 | When `last_seen_at` is written, including on a clean stop | `TestLastSeen`, `TestCloseAllOnStop`, `TestMigrationAddsLastSeen` | ✅ |
| R25 | No credential in any output, log, answer, or file other than `credential.json` | `TestCredentialNeverLogged`, `TestListHasNoCredential`, `TestCredentialNeverInLogOrState`, `TestNoCredentialPrinted`, `TestNoCredentialInPairOutput`, `TestNoSecretsEndToEnd` | ✅ |
| R26 | Another user is refused even if it could reach the socket | `TestOtherUserRefused`, `TestRuntimeFolderPrivate` | ✅ |
| R28 | `tervi pair` checks before contacting the server | `TestInstalledCheck`, `TestPairInstalledCheckFirst` | ✅ |
| R29 | A service that can't start, or linger that can't be turned on, keeps the pairing | `TestEnsureRunningFailures`, `TestEnsureRunningTurnsOnLinger`, `TestConnectServiceFails`, `TestPairServiceFails` | ✅ |
| R31 | The commands return while the daemon keeps running | `TestConnectReturnsAfterLastLine`, `TestPairReturnsAfterLastLine` | ✅ |
| R33 | Pairing again only after a rejection; otherwise the slice-1 refusal | `TestPairAgainAfterRejection`, `TestAlreadyPairedStillRefused`, `TestPairAgainDeleteFails` | ✅ |
| R34 | A `500` logs what kind of operation failed, and never the error's details, a value, or a credential | `TestInternalErrorLogsKindOnly`, `TestPairingInternalErrorLogged`, `TestDatabaseFailureIsNotRejection`, `TestListDatabaseFailure` | ✅ |
| R35 | The file and folder modes, the atomic write, an unwritable folder stopping `tervi pair` before the server, the replaced messages, and the real file in the end-to-end test | `TestFileBackendSavesPrivateFile`, `TestFileBackendMissingIsNotFound`, `TestPairUnwritableCredentialStopsBeforeServer`, `TestCredentialFileMessages`, `TestNoSecretsEndToEnd` | ✅ |
| R36 | Group or other permissions, or another owner, are refused with the two lines, and the file's mode is never changed | `TestFileBackendRefusesOpenPermissions`, `TestFileBackendRefusesOtherOwner`, `TestUnprotectedCredentialMessages`, `TestUnprotectedCredentialIsFailedTry` | ✅ |

Rows 43–44 freeze the daemon with a signal instead of cutting the network;
for the server that is the same: the connection stays open and goes silent.
Slice 1's hand checks not repeated here (wrong codes in two tabs, losing the
server during pairing, the Swagger page) did not change in this slice.

Tests run: `go vet ./...`, `go test -count=1 ./...`, and
`node --test internal/server/web/static/machines-view.test.js` on 7114128 →
all pass (every Go package ok; 9 of 9 page tests).
