# 0001 Pairing — checklist

Spec: [spec.md](spec.md)
Tried on: 2026-10-11 · Commit: 45c5950

## Before you start

You need two terminals and a browser, all on the desktop itself: the secret
store (GNOME Keyring) is usually unavailable over SSH.

- **Terminal 1** runs the server.
- **Terminal 2** runs the worker, `tervi pair`.
- **The browser** opens the approval links.

First, in terminal 2, check that this computer is not paired yet:

```bash
ls ~/.config/tervi/state.json
```

It should say `No such file or directory`. If the file exists, tell the
architect before you go on.

## Check by hand

Do the rows in order: each one can depend on the state the row before left.
In the commands, type `go run ./cmd/tervi` in place of `tervi`: for example,
`tervi pair --server http://localhost:8080` becomes
`go run ./cmd/tervi pair --server http://localhost:8080`.

**Starting the server**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | R32 | Terminal 1: `docker compose up -d`, then `go run ./cmd/server migrate` | `Database is up to date (version 1).` | ✅ |
| 2 | R1 | Terminal 2: `tervi pair` (no `--server`) | `Usage: tervi pair --server <url>`, and it stops | ✅ |
| 3 | R4 | Terminal 2, with the server **not** started yet: `tervi pair --server http://localhost:8080` | `✗ Can't reach http://localhost:8080. Check that the server is running, then run the command again.` | ✅ |
| 4 | — | Terminal 1: `TERVI_API_DOCS=on go run ./cmd/server`. Open `http://localhost:8080/docs` | The server keeps running. The browser shows the Swagger page with every route | ✅ |
| 5 | R21 | Terminal 2: `curl -i -H 'Host: evil.example' http://localhost:8080/health` | `HTTP/1.1 403 Forbidden` and `{"error": "unknown_host"}` | ✅ |

**Rejecting a pairing**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 6 | R5 | Terminal 2: `tervi pair --server http://localhost:8080` | `Pairing this computer with http://localhost:8080`, your computer name, your OS, a link, and `Waiting for approval... (expires at HH:MM)`, about 10 minutes from now | ✅ |
| 7 | R6 | Open the link in the browser. Then reload the page | `Pair a computer?` with the same computer name and OS as the terminal, and **Accept** / **Reject**. After the reload: the same page, nothing changed | ✅ |
| 8 | R9 | Click **Reject** | Browser: `Rejected. This computer was not paired.` Terminal, within a few seconds: `✗ Pairing was rejected in the browser.`, and it stops | ✅ |
| 9 | R16 | Reload the page | Only `Rejected. This computer was not paired.` No buttons | ✅ |

**Wrong codes**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 10 | — | Terminal 2: `tervi pair --server http://localhost:8080`. Open the link in **two** browser tabs | Both tabs show **Accept** / **Reject** | ✅ |
| 11 | R7 | In tab 1, click **Accept** | Tab 1: `Type this code into the terminal of <your computer>:`, an 8-digit code like `4827-1934`, and `Never give this code to anyone.` | ✅ |
| 12 | R12, R8 | Look at terminal 2 | `✓ Approved in the browser.`, then `Type the code shown in the browser (expires at HH:MM, 5 tries left):` with the **same** time as row 10. The code itself is **not** in the terminal | ✅ |
| 13 | R10 | In tab 2, which still shows the buttons, click **Reject** | Tab 2 shows the same code as tab 1, not `Rejected` | ✅ |
| 14 | R11 | Reload tab 1 | The same code as before | ✅ |
| 15 | R13 | Terminal 2: type a wrong code, for example `0000-0000` | `✗ Wrong code. 4 tries left.` | ✅ |
| 16 | R13 | Type 4 more wrong codes | After the fifth: `✗ Too many wrong codes. Pairing failed. Run the command again.` Within a few seconds, the browser by itself: `✗ Pairing failed: the wrong code was typed 5 times. Run tervi pair on the computer again to start over.` | ✅ |

**Cancelling**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 17 | R23 | Terminal 2: `tervi pair --server http://localhost:8080`, then press **Ctrl+C** at once | `Pairing cancelled. Nothing was saved.`, and it stops at once | ✅ |

**Losing the connection, then pairing**

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 18 | R24 | Terminal 2: `tervi pair --server http://localhost:8080`. Then stop the server in terminal 1 with **Ctrl+C** | Terminal 2: `Connection lost, retrying...`, and it keeps waiting | ✅ |
| 19 | R24 | Start the server again in terminal 1 | Terminal 2: `Connection restored.`, and it keeps waiting for approval | ✅ |
| 20 | R17, R19 | Open the link, click **Accept**, and type the code into terminal 2 | Terminal: `✓ Code accepted.`, `✓ Credential saved.`, `Confirming with the server...`, `✓ Paired successfully.` Browser: `✓ <your computer> is paired.`, your OS, and `You can close this tab.` | ✅ |
| 21 | R18 | Terminal 2: `cat ~/.config/tervi/state.json` | Only three fields: `machine_id`, `"credential_saved": true`, and `"confirmed": true`. **No credential** (no long random string) | ✅ |
| 22 | R18 | Look back over everything terminals 1 and 2 printed | No credential, no pairing code in terminal 2, and no long random string anywhere | ✅ |
| 23 | R2 | Terminal 2: `tervi pair --server http://localhost:8080` again | `This computer is already paired with http://localhost:8080. Nothing was changed.` | ✅ |

Row 4 checks a decision in the spec (the Swagger page), not a requirement.

## Proved by tests

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| R3 | An unusable secret store stops the worker before it contacts the server | `TestStoreCheck`, `TestUnusableStoreStopsBeforeFlow` | ✅ |
| R14 | A code is accepted only by the pairing that created it | `TestCodeOnlyForItsPairing` | ✅ |
| R15 | After 10 minutes the link, the polling, and the code all expire, on both sides | `TestPollAfterExpiry`, `TestExpiredCannotBeDecided`, `TestNoCodeWhenFinished`, `TestCodeDeadline`, `TestDeadlineDuringOutage`, `TestCleanupPass` | ✅ |
| R16 | The code part: a rejected, expired, or failed pairing accepts no code | `TestNoCodeWhenFinished`, `TestFailedShowsReason` | ✅ |
| R18 | The parts you can't see: the credential is in the secret store, never in logs or any output, in every case | `TestStoreSaveVerified`, `TestEntryJSONHoldsRealCredential`, `TestSecretNeverPrinted`, `TestCredentialNeverPrinted`, `TestNoSecretInOutput`, `TestNoSecretEverPrinted` | ✅ |
| R20, R25 | A failed save says pairing was not completed, and the browser shows it failed | `TestSaveFails`, `TestSaveFailure` | ✅ |
| R21 | The server listens only on the computer itself; every wrong host is refused | `TestListenAddressMustBeLoopback`, `TestHostCheck` | ✅ |
| R22 | The database holds only hashes of the keys and the credential | `TestStartStoresOnlyHashes`, `TestCredentialOnlyHashed`, `TestHashColumnsAreUnique` | ✅ |
| R23 | The other Ctrl+C cases: during the code, and after saving began | `TestCtrlCDuringCodePhase`, `TestCtrlCAfterSaving`, `TestCtrlCAfterSavingThenFinish` | ✅ |
| R26 | No confirmation within 5 minutes → failed, and the credential never works | `TestAcknowledgmentAfterExpiry`, `TestCleanupPass` | ✅ |
| R27 | A computer is not shown as paired until it confirms | `TestNotPairedUntilConfirmed`, `TestAcknowledgment` | ✅ |
| R28 | A lost confirmation is tried 3 times, then the finish command is shown | `TestAckRetries`, `TestLostAcknowledgmentThenFinish` | ✅ |
| R29 | The next run finishes an unconfirmed pairing, or cleans up and starts again | `TestFinishEarlierPairing`, `TestFinishEarlierResults`, `TestAckResults` | ✅ |
| R30 | The credential never goes to a server other than its own | `TestStepZeroOtherServerChangesNothing`, `TestFinishEarlierPairing` | ✅ |
| R31 | Incomplete or damaged local data is removed and reported | `TestDamagedEntryWins`, `TestLeftoverThenStartNew`, `TestLeftoverDeleteFails` | ✅ |
| R32 | The refusal part: the server won't start on a database at the wrong version | `TestRunRefusesUnmigratedDatabase`, `TestCheckVersion` | ✅ |

Tests run: `go test ./...` on 45c5950 → all 14 packages ok
