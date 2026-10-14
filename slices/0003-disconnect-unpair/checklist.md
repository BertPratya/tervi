# 0003 Disconnect and unpair — checklist

Spec: [spec.md](spec.md)
Tried on: — · Commit: —

This checklist starts from an empty database, with this computer paired
again by slice 3's `tervi`. It disconnects and connects again, unpairs,
pairs again, revokes from the Machines page, and pairs once more.

## Before you start

- **Terminal 1** runs the server.
- **Terminal 2** runs `tervi` commands.
- **The browser** opens `http://localhost:8080/machines` and the approval
  links.

Over SSH, open terminal 1 with `ssh -L 8080:localhost:8080 <this computer>`,
as in slice 2.

In terminal 2, from the project folder:

```bash
git pull                          # the slice branch, slice/0003-disconnect-unpair
docker compose up -d
go install ./cmd/tervi
tervi unpair --local-only --yes   # removes this computer's slice-2 pairing
docker compose down -v            # deletes the database and its data
docker compose up -d              # a new, empty database
go run ./cmd/server migrate       # should say: Database is up to date (version 3).
```

In terminal 1: `go run ./cmd/server`. In terminal 2, pair this computer
again with `tervi pair --server http://localhost:8080`, Accept in the
browser, and type the code. `tervi status` should then show
`● Connected to http://localhost:8080`.

Starting from an empty database means this computer is paired with
slice 3's `tervi`, so the server knows its identity from the start.

## Check by hand

Do the rows in order: each one can depend on the state the row before left.

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R1, R8 | `tervi disconnect`, and watch the Machines page | `✓ Disconnected. tervi stays in the background but won't connect.` / `  Connect again with: tervi connect`; within about 5 seconds the page shows `Offline · last seen less than a minute ago` | ⬜ |
| 2 | R1 | `tervi logs` | The last line is `○ Disconnected.` | ⬜ |
| 3 | R7 | `tervi status` | `○ Disconnected.` / `  Connect with: tervi connect` | ⬜ |
| 4 | R4 | `tervi disconnect` again | `Already disconnected.` | ⬜ |
| 5 | — | Restart the computer and log in. In terminal 1 (over SSH, reconnect with the `-L 8080:localhost:8080` forward), from the project folder: `docker compose up -d`, then `go run ./cmd/server` | The server starts | ⬜ |
| 6 | R2 | Open the Machines page | This computer is `Offline` | ⬜ |
| 7 | R2, R7 | `tervi status` | `○ Disconnected.` / `  Connect with: tervi connect` | ⬜ |
| 8 | R9 | `tervi connect` | `✓ Connected …` as in slice 2; the page shows `Online` | ⬜ |
| 9 | — | `systemctl --user stop tervi` | Nothing printed | ⬜ |
| 10 | R3 | `tervi disconnect` | `✓ Disconnected. tervi won't connect, including after the computer restarts.` / `  Connect again with: tervi connect` | ⬜ |
| 11 | R2 | `systemctl --user start tervi`, then watch the page for 15 seconds | The page stays `Offline` | ⬜ |
| 12 | R7 | `tervi status` | `○ Disconnected.` | ⬜ |
| 13 | R9 | `tervi connect` | `✓ Connected …`; the page shows `Online` | ⬜ |
| 14 | R10 | `tervi unpair`, answer `n` | `This removes this computer's pairing with http://localhost:8080.` / `tervi will stop running in the background. Continue? [y/N]`, then `Nothing was changed.`; the page still shows `Online` | ⬜ |
| 15 | — | Stop the server: Ctrl+C in terminal 1 | The server stops | ⬜ |
| 16 | R12 | `tervi unpair --yes` | `✗ No answer from http://localhost:8080.` / `  The server may or may not have unpaired this computer. Nothing was removed here.` / `  Run tervi unpair again to finish, or revoke this computer on the Machines page.` | ⬜ |
| 17 | R12 | `tervi status` | `○ Reconnecting to http://localhost:8080`: still paired | ⬜ |
| 18 | — | Start the server again: `go run ./cmd/server` in terminal 1 | Within a few seconds the page shows this computer `Online` | ⬜ |
| 19 | R11 | `tervi unpair`, answer `y` | `✓ Unpaired. This computer is no longer paired with http://localhost:8080.` / `  Linger is still on. Turn it off with: loginctl disable-linger` | ⬜ |
| 20 | R19 | Look at the Machines page | This computer is gone | ⬜ |
| 21 | R11 | `systemctl --user status tervi` | `Unit tervi.service could not be found.` | ⬜ |
| 22 | R11 | `ls -a ~/.config/tervi/` | No `credential.json`, `state.json`, `.credential-…`, or `credential-check.json` | ⬜ |
| 23 | R11 | `which tervi` | Still prints its path: the program stays installed | ⬜ |
| 24 | R15 | `tervi unpair` | `This computer isn't paired. Nothing was changed.` | ⬜ |
| 25 | R24 | `tervi pair --server http://localhost:8080`, and open the approval link | `This looks like a computer that was paired before:` / `<name>, unpaired on <today's day and month>.`, with `[ Accept ]  brings back <name>, with its history` and `[ Reject ]  doesn't pair it` | ⬜ |
| 26 | R26 | Click **Accept**, and type the code in terminal 2 | The page lists **one** entry for this computer, `Online` | ⬜ |
| 27 | R17 | Click **Revoke** on this computer, then **Cancel** | The question `Revoke <name>? It stops working at once and must be paired again to come back.` with **Revoke** and **Cancel**; Cancel changes nothing | ⬜ |
| 28 | — | Stop the server: Ctrl+C in terminal 1 | The server stops | ⬜ |
| 29 | R18 | On the still-open page, click **Revoke**, then **Revoke** | The row stays, and the page shows `Couldn't revoke <name>. Try again.` (after the next refresh, `Can't reach the server. Trying again…`) | ⬜ |
| 30 | — | Start the server again | The page shows this computer `Online` again | ⬜ |
| 31 | R18 | Click **Revoke**, then **Revoke** | The row disappears at once | ⬜ |
| 32 | R20 | `tervi logs` | The last line is `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server http://localhost:8080` | ⬜ |
| 33 | R21 | `tervi status; echo $?` | `✗ This computer was unpaired on the server.` / `  Pair it again with: tervi pair --server http://localhost:8080`, then `1` | ⬜ |
| 34 | R21 | `tervi connect; echo $?` | The same two lines, then `1` | ⬜ |
| 35 | R21 | `tervi disconnect; echo $?` | The same two lines, then `0` | ⬜ |
| 36 | R22 | `tervi pair --server http://localhost:8080` | The first line is `This computer was unpaired on the server. Starting a new pairing.` | ⬜ |
| 37 | R24, R26 | Open the approval link, click **Accept**, and type the code | The page says `<name>, unpaired on <today's day and month>.`; afterwards the Machines page lists **one** entry for this computer, `Online` | ⬜ |
| 38 | R14 | `tervi unpair --local-only`, answer `y` | `This removes this computer's pairing without telling http://localhost:8080.` / `If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]`, then `✓ Unpaired on this computer only.` and the linger line | ⬜ |
| 39 | R14 | Look at the Machines page | This computer is still listed, `Offline` | ⬜ |
| 40 | R6 | `tervi disconnect` | `This computer isn't paired. Pair it with: tervi pair --server <url>` | ⬜ |
| 41 | R18 | Revoke this computer on the page | The row disappears | ⬜ |
| 42 | — | Pair again to leave the computer paired: `tervi pair --server http://localhost:8080`, Accept, type the code | The page shows it `Online`, recognized as before | ⬜ |

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R1, R2, R7, R8, R9 | Disconnect and connect with the real server, including a daemon started afterwards staying idle | `TestDisconnectEndToEnd`, `TestDisconnectWithoutDaemonEndToEnd`, `TestDisconnectConnected`, `TestDisconnectReconnecting`, `TestDisconnectedAfterRestart`, `TestDisconnectLogLine`, `TestDisconnectDuringConnect` | ⬜ |
| R3 | No daemon is started; the lock keeps a starting daemon out while the file is written | `TestDisconnectNoDaemon`, `TestWithLockBusy` | ⬜ |
| R4, R5, R6 | Each case, both with and without a daemon | `TestDisconnectAlready`, `TestDisconnectRejectedOrRevoked`, `TestDisconnectNotPaired` | ⬜ |
| R7 | `○ Disconnected.` from the daemon and from `state.json` | `TestStatusDisconnected`, `TestStatusNoDaemonConnectTrue`, `TestDisconnectThenStatus` | ⬜ |
| R10 | Every answer to the question | `TestUnpairAsks`, `TestUnpairYesSkipsQuestion` | ⬜ |
| R11 | The order of removal, the credential sent, the linger line, the server's side | `TestUnpairRemovesInOrder`, `TestUnpairLingerLine`, `TestUnpairRevokes`, `TestUnpairClosesConnection`, `TestUnpairEndToEnd` | ⬜ |
| R12 | No answer and other answers remove nothing; repeating finishes | `TestUnpairNoAnswer`, `TestUnpairOtherAnswer`, `TestUnpairRepeat` | ⬜ |
| R13 | A credential the server already refuses | `TestUnpairServerAlreadyRefuses`, `TestUnpairUnknown` | ⬜ |
| R14, R15 | `--local-only` sends nothing; not paired; leftovers, also with a damaged or exposed `credential.json`, which cannot be caused safely by hand; an unfinished pairing; no copy of the secret is left, including a crash's temporary file | `TestUnpairLocalOnly`, `TestUnpairNotPaired`, `TestUnpairLeftovers`, `TestUnpairUnfinishedPairing`, `TestUnpairRemovesSecretLeftovers`, `TestDeleteLeftovers` | ⬜ |
| R16 | Each failure says what is left, and a second run finishes | `TestUnpairServiceFailure`, `TestUnpairDeleteFailure` | ⬜ |
| R17 | The question text and the dialog | `machines-view.test.js`, `TestMachinesPageHasRevokeDialog` | ⬜ |
| R18 | Revoking, closing with `4002` (also for a connection opened at that moment), repeats, unknown IDs, and the page after each answer | `TestRevokeMachine`, `TestRevokeClosesConnection`, `TestRevokeRepeatAndUnknown`, `TestCloseRevoked`, `TestRevokedWhileOpening`, `machines-view.test.js`, `TestRevokeConnectedEndToEnd` | ⬜ |
| R19 | Ended machines are not listed but stay in the database; the migration keeps every machine | `TestListOnlyMachinesWithActiveCredential`, `TestMigrationMovesCredentials` | ⬜ |
| R20 | `4002` and `credential_revoked` both stop the daemon, also when it was offline | `TestDaemonStopsOnRevokedAnswer`, `TestDaemonStopsOnCloseRevoked`, `TestConnectionRefusesRevoked`, `TestRevokeOfflineEndToEnd` | ⬜ |
| R21 | The unpaired message in status, connect, and disconnect | `TestStatusRevoked`, `TestConnectRevokedMessage`, `TestDisconnectRejectedOrRevoked` | ⬜ |
| R22 | Pairing after a revocation | `TestPairAfterRevoked` | ⬜ |
| R23 | The identity's value, that it is never shown, and the question without a machine ID (which needs root to cause by hand) | `TestIdentityKnownValue`, `TestIdentityParsing`, `TestStartSendsIdentity`, `TestIdentityNeverShown`, `TestNoMachineIDQuestion`, `TestNoMachineIDYes`, `TestStartStoresIdentity`, `TestStartRejectsBadIdentity` | ⬜ |
| R24, R25, R28 | Which machine matches, decided once, and the page's lines | `TestMatchPairedBefore`, `TestMatchMostRecent`, `TestMatchDecidedOnce`, `TestApprovalShowsEarlierMachine`, `view.test.js` | ⬜ |
| R26, R27 | Accept brings the machine back and ends its earlier credential; failures leave it alone | `TestCodeAttachesToMatchedMachine`, `TestAcknowledgeBringsMachineBack`, `TestAcknowledgeClosesEarlierConnection`, `TestSaveFailureKeepsEarlierCredential`, `TestPairSameComputerAgainEndToEnd` | ⬜ |
| R29 | The identity depends only on the machine ID | `TestIdentitySameEachTime` | ⬜ |
| R30 | Revoke needs no proof | `TestRevokeNeedsNoProof` | ⬜ |

Tests run: `go test ./...` on — → —
