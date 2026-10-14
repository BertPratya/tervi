# 0003 Disconnect and unpair — checklist

Spec: [spec.md](spec.md)
Tried on: — · Commit: —

This checklist starts where slice 2 ended: this computer paired and
connected. It disconnects and connects again, unpairs, pairs again, revokes
from the Machines page, and pairs once more.

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
go run ./cmd/server migrate       # should say: Database is up to date (version 3).
go install ./cmd/tervi
systemctl --user restart tervi    # the daemon now runs the new program
```

In terminal 1: `go run ./cmd/server`. Then `tervi status` in terminal 2
should show `● Connected to http://localhost:8080`.

**This computer was paired before this slice,** so the server has no
identity for it yet (plan, "Moving existing machines"). The first pairing
again (row 11) is therefore not recognized; the second one (row 15) is.

## Check by hand

Do the rows in order: each one can depend on the state the row before left.

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R1, R8 | `tervi disconnect`, and watch the Machines page | `✓ Disconnected. tervi stays in the background but won't connect.` / `  Connect again with: tervi connect`; within about 5 seconds the page shows `Offline · last seen less than a minute ago` | ⬜ |
| 2 | R7 | `tervi status` | `○ Disconnected.` / `  Connect with: tervi connect` | ⬜ |
| 3 | R4 | `tervi disconnect` again | `Already disconnected.` | ⬜ |
| 4 | R2 | Restart the computer, log in, open the Machines page again | The computer stays `Offline`; `tervi status` still shows `○ Disconnected.` | ⬜ |
| 5 | R9 | `tervi connect` | `✓ Connected …` as in slice 2; the page shows `Online` | ⬜ |
| 6 | R3 | `systemctl --user stop tervi`, then `tervi disconnect` | `✓ Disconnected. tervi won't connect, including after the computer restarts.` / `  Connect again with: tervi connect` | ⬜ |
| 7 | R2, R7 | `systemctl --user start tervi`, wait 15 seconds, then `tervi status` | The page stays `Offline`; `○ Disconnected.`. Then `tervi connect` to connect again | ⬜ |
| 8 | R10 | `tervi unpair`, answer `n` | `This removes this computer's pairing with http://localhost:8080.` / `tervi will stop running in the background. Continue? [y/N]`, then `Nothing was changed.`; still `Online` | ⬜ |
| 9 | R12 | Stop the server (Ctrl+C in terminal 1), then `tervi unpair --yes` | `✗ No answer from http://localhost:8080.` and the two lines after it; `tervi status` shows it still paired (reconnecting). Start the server again; it connects | ⬜ |
| 10 | R11, R19 | `tervi unpair`, answer `y` | `✓ Unpaired. This computer is no longer paired with http://localhost:8080.` / `  Linger is still on. Turn it off with: loginctl disable-linger`; the computer disappears from the page; `systemctl --user status tervi` says the unit could not be found; `ls ~/.config/tervi/` shows no `credential.json` or `state.json`; `which tervi` still prints its path | ⬜ |
| 11 | R15, R28 | `tervi unpair`, then `tervi pair --server http://localhost:8080` and open the approval link | `This computer isn't paired. Nothing was changed.`; the approval page is exactly as in slice 1, with no "paired before" lines (this computer had no identity yet). Accept, type the code; the page lists the computer `Online` | ⬜ |
| 12 | R17 | On the Machines page, click **Revoke** on this computer, then **Cancel** | `Revoke <name>? It stops working at once and must be paired again to come back.` with **Revoke** and **Cancel**; Cancel changes nothing | ⬜ |
| 13 | R18, R20 | Click **Revoke**, then **Revoke**; then `tervi logs` | The row disappears at once; the log ends with `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server http://localhost:8080` | ⬜ |
| 14 | R21 | `tervi status`, then `tervi connect`, then `tervi disconnect` | Each prints `✗ This computer was unpaired on the server.` / `  Pair it again with: tervi pair --server http://localhost:8080`; status and connect exit with `1` (`echo $?`), disconnect with `0` | ⬜ |
| 15 | R22, R24, R26 | `tervi pair --server http://localhost:8080`, open the approval link | The terminal first says `This computer was unpaired on the server. Starting a new pairing.`; the page says `This looks like a computer that was paired before:` / `<name>, unpaired on <today's date>.` with `[ Accept ]  brings back <name>, with its history` and `[ Reject ]  doesn't pair it`. Accept, type the code; the page lists **one** entry for this computer, `Online` | ⬜ |
| 16 | R14 | `tervi unpair --local-only`, answer `y` | `This removes this computer's pairing without telling http://localhost:8080.` / `If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]`, then `✓ Unpaired on this computer only.`; the page still lists the computer (Offline) | ⬜ |
| 17 | R6, R18 | `tervi disconnect`; then Revoke the computer on the page | `This computer isn't paired. Pair it with: tervi pair --server <url>`; the row disappears | ⬜ |
| 18 | — | Pair again to leave the computer paired: `tervi pair --server http://localhost:8080`, Accept, type the code | The page shows it `Online`, recognized as before | ⬜ |

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R1, R2, R7, R8, R9 | Disconnect and connect with the real server, including a daemon started afterwards staying idle | `TestDisconnectEndToEnd`, `TestDisconnectWithoutDaemonEndToEnd`, `TestDisconnectConnected`, `TestDisconnectReconnecting`, `TestDisconnectedAfterRestart` | ⬜ |
| R3 | No daemon is started; the lock keeps a starting daemon out while the file is written | `TestDisconnectNoDaemon`, `TestWithLockBusy` | ⬜ |
| R4, R5, R6 | Each case, both with and without a daemon | `TestDisconnectAlready`, `TestDisconnectRejectedOrRevoked`, `TestDisconnectNotPaired` | ⬜ |
| R7 | `○ Disconnected.` from the daemon and from `state.json` | `TestStatusDisconnected`, `TestStatusNoDaemonConnectTrue`, `TestDisconnectThenStatus` | ⬜ |
| R10 | Every answer to the question | `TestUnpairAsks`, `TestUnpairYesSkipsQuestion` | ⬜ |
| R11 | The order of removal, the credential sent, the linger line, the server's side | `TestUnpairRemovesInOrder`, `TestUnpairLingerLine`, `TestUnpairRevokes`, `TestUnpairClosesConnection`, `TestUnpairEndToEnd` | ⬜ |
| R12 | No answer and other answers remove nothing; repeating finishes | `TestUnpairNoAnswer`, `TestUnpairOtherAnswer`, `TestUnpairRepeat` | ⬜ |
| R13 | A credential the server already refuses | `TestUnpairServerAlreadyRefuses`, `TestUnpairUnknown` | ⬜ |
| R14, R15 | `--local-only` sends nothing; not paired; leftovers | `TestUnpairLocalOnly`, `TestUnpairNotPaired`, `TestUnpairLeftovers` | ⬜ |
| R16 | Each failure says what is left, and a second run finishes | `TestUnpairServiceFailure`, `TestUnpairDeleteFailure` | ⬜ |
| R17 | The question text and the dialog | `machines-view.test.js`, `TestMachinesPageHasRevokeDialog` | ⬜ |
| R18 | Revoking, closing with `4002`, repeats, unknown IDs | `TestRevokeMachine`, `TestRevokeClosesConnection`, `TestRevokeRepeatAndUnknown`, `TestCloseRevoked`, `TestRevokeConnectedEndToEnd` | ⬜ |
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
