# 0001 Pairing — slice 1

Status: approved

## What the user does

1. On the computer to pair, the user runs:

   ```text
   $ tervi pair --server https://tervi.example.com

   Pairing this computer with https://tervi.example.com
     Computer: bert-desktop
     OS:       Ubuntu 26.04

   Open this link (on any device) and approve this computer:
     https://tervi.example.com/pair/k7Fq...x2

   Waiting for approval... (expires at 14:32)
   ```

2. The user opens the link on any device. No sign-in is needed. The page shows
   the computer name and OS, with **Pair** and **Reject** buttons.
3. The user clicks **Pair**. The page shows a pairing code:

   ```text
   Type this code into the terminal of bert-desktop:
           4827-1934
   Never give this code to anyone.
   ```

4. The terminal notices the approval by itself and asks for the code:

   ```text
   ✓ Approved in the browser.
   Type the code shown in the browser (expires at 14:32):
   Code: 4827-1934
   ✓ Paired successfully.
   ```

5. The page changes to: `✓ bert-desktop is paired. You can now close this tab.`

## Whole design (all slices, short)

A pairing moves through these states:

```text
waiting for approval ──Pair──► waiting for code ──correct code──► finishing ──computer confirms──► paired
        │                           │                                  │
        ├──Reject──► rejected       ├──5 wrong codes──► failed         └──no confirmation in time──► unfinished (slice 2)
        └──10 minutes──► expired ◄──┘ (10 minutes)
```

Four secrets are involved:

| Secret | Who has it | What it allows |
| --- | --- | --- |
| Approval link | Whoever the terminal shows it to | See the request; click Pair or Reject |
| Worker's own secret | The worker, in memory, for this attempt only | Check the pairing's progress |
| Pairing code | Shown only in the browser, typed into the terminal | Prove the approver is at the computer being paired |
| Credential | The worker, saved in the OS secret store | Act as this paired computer from now on |

Later slices add: cleanup when finishing fails (2), crash recovery (3), HTTPS
and other real-network protections (4), Windows (5), and sign-in (later).

## Must always be true (this slice)

**Starting**

- R1. When `tervi pair` runs without `--server`, the worker shall show
  `Usage: tervi pair --server <url>` and stop.
- R2. If this computer is already paired, then the worker shall say which
  server it is paired with, change nothing, and stop.
- R3. If the OS secret store cannot be used, then the worker shall say so and
  stop, before contacting the server.
- R4. If the server cannot be reached when starting, then the worker shall say
  so and stop.
- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting).

**Approving**

- R6. When someone opens the approval link, the server shall show the computer
  name and OS, as reported by the worker, with Pair and Reject. Opening the
  link shall not change the pairing.
- R7. When Pair is clicked on a pairing that is waiting for approval, the server
  shall show a pairing code in the browser.
- R8. The worker shall never show the pairing code. It appears only in the browser.
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R10. Once Pair or Reject has been clicked, later clicks on either button,
  from any tab or device, shall not change the decision. The page shall show
  the current state instead.
- R11. When the link is opened again, the page shall show the current state:
  the buttons while waiting for approval, the same code while waiting for the
  code, and otherwise the result (paired, rejected, failed, or expired).

**Entering the code**

- R12. The worker shall ask for the code only after the pairing is approved,
  and shall show the same expiry time again.
- R13. If a wrong code is typed, then the worker shall show how many tries are
  left. After 5 wrong codes, the pairing shall fail, and both the terminal and
  the browser shall say so.
- R14. A pairing code shall only work for the pairing that created it.

**Deadline**

- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired.
- R16. While a pairing is expired, rejected, or failed, the link shall show only
  the result, and no code shall be accepted.

**Finishing**

- R17. When the correct code is typed, the worker shall receive its credential.
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs.
- R19. When the credential is saved, the worker shall confirm with the server.
  Then the terminal shall show `✓ Paired successfully.`, and the browser shall
  show that the computer is paired and the tab can be closed.
- R20. If saving the credential fails, then the worker shall say that pairing
  was not completed.

**Secrets**

- R21. Having only the approval link shall not be enough to get the credential.
- R22. The server shall store only hashes of the secrets, never the secrets themselves.

**Cancelling**

- R23. When the user presses Ctrl+C, the worker shall show
  `Pairing cancelled. Nothing was saved.` and stop. The pairing then expires on
  its own.

**Network**

- R24. If the connection to the server is lost while waiting for approval, then
  the worker shall show `Connection lost, retrying...`, keep trying until the
  deadline, and continue normally when the connection returns.

## Not in this slice

- Sign-in. Anyone with the link can approve; the pairing code limits the risk.
- Cleanup when finishing fails after the correct code (slice 2).
- Crash recovery, re-pairing, and unpairing (slice 3).
- HTTPS. Slice 1 runs only on one computer (`localhost`); HTTPS comes in slice 4,
  before tervi is used over a real network.
- Windows and macOS (slice 5 and later).
- Pairing one computer with more than one server.
- Keeping a connection open after pairing.

## Open questions

None.

## Decisions

- `tervi pair --server <url>` — the word "pair" says what it does and leaves
  room for other commands later.
- Approve through a link, with no sign-in in this slice — sign-in doesn't exist
  yet, and the pairing code blocks the main attack.
- The browser shows the code and the user types it into the terminal — this
  blocks the "attacker sends you a link" attack (device code phishing): only
  someone at the computer being paired can finish. Typing the code into the
  browser, or comparing two codes, does not block it.
- The terminal asks for the code only after approval — the user can't type it
  too early.
- Ctrl+C lets the pairing expire instead of cancelling it on the server —
  expiry is needed anyway for crashes and power loss, when the program can't
  send anything.
- Show the expiry as a fixed clock time, not a live countdown — nothing moves
  on screen, typing the code is never disturbed, and the output stays readable
  when saved to a log file.
- The 10-minute deadline covers only the human steps — finishing takes seconds,
  and a deadline there would fail someone who types the code at minute 9:59.
- Retry until the deadline when the connection drops while waiting for
  approval — the worker is already asking the server every few seconds, and a
  short network blip shouldn't force the user to start over.
- Check the secret store before contacting the server — no link is created that
  could never finish.
- The credential lives in the OS secret store, never in a file — other programs
  can read or copy files.
- An already-paired computer refuses to pair again — re-pairing needs crash
  recovery and unpairing, which come in later slices.
