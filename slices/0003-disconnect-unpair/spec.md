# 0003 Disconnect and unpair — slice 3

Status: draft

## What the user does

1. The computer is paired and connected, as at the end of slice 2. The user
   pauses it:

   ```text
   $ tervi disconnect
   ✓ Disconnected. tervi stays in the background but won't connect.
     Connect again with: tervi connect
   $
   ```

   Within about 5 seconds the Machines page shows the computer
   `Offline · last seen less than a minute ago`.

2. The user checks it:

   ```text
   $ tervi status
   ○ Disconnected.
     Connect with: tervi connect
   ```

3. The user restarts the computer and logs in. tervi starts in the background
   but stays disconnected; the Machines page still shows Offline.

4. The user connects again with `tervi connect`, exactly as in slice 2. The
   page shows Online.

5. Later, the user unpairs the computer:

   ```text
   $ tervi unpair
   This removes this computer's pairing with http://localhost:8080.
   tervi will stop running in the background. Continue? [y/N] y
   ✓ Unpaired. This computer is no longer paired with http://localhost:8080.
   $
   ```

   The computer disappears from the Machines page. tervi no longer runs in
   the background, also after a restart. The `tervi` program stays installed.

6. On another computer, the user clicks **Revoke** on the Machines page and
   confirms. That computer disappears from the page at once. On it,
   `tervi status` shows:

   ```text
   ✗ This computer was unpaired on the server.
     Pair it again with: tervi pair --server http://localhost:8080
   ```

*(Recognizing a computer that pairs again: still to be written.)*

## Whole design (all slices, short)

Slice 2 keeps a paired computer connected. This slice adds the ways to stop:
pausing (`tervi disconnect`, which keeps the pairing), and ending the pairing
from either side (`tervi unpair` on the computer, revoking on the Machines
page). It also lets the server recognize a computer that pairs again.

| Action | The pairing | The background service | After a reboot |
| --- | --- | --- | --- |
| `tervi disconnect` | Kept | Keeps running, idle | Stays disconnected |
| `systemctl --user stop tervi` (slice 2) | Kept | Stopped | Connects again |
| `tervi unpair` | Removed | Stopped and removed | Nothing runs |

## Must always be true (this slice)

**Disconnecting**

- R1. When the user runs `tervi disconnect` on a paired computer that is
  connected or reconnecting, the worker shall close the connection, stop
  trying to connect, keep the pairing, and print
  `✓ Disconnected. tervi stays in the background but won't connect.` then
  `Connect again with: tervi connect`, and exit with `0`. The daemon shall
  keep running.
- R2. While the user has disconnected, the daemon shall not connect, also
  after the computer restarts, until the user runs `tervi connect`.
- R3. When `tervi disconnect` runs and tervi is not running in the
  background, the worker shall still record that it must not connect, print
  `✓ Disconnected. tervi won't connect, including after you log in again.`
  then `Connect again with: tervi connect`, and exit with `0`. It shall not
  start tervi in the background.
- R4. When `tervi disconnect` runs on a computer the user already
  disconnected, the worker shall print `Already disconnected.`, change
  nothing, and exit with `0`.
- R5. When `tervi disconnect` runs on a computer whose credential was
  rejected (slice 2's R18), the worker shall change nothing, print
  `Not connecting: the server doesn't accept this computer's credential.`
  then `Pair again with: tervi pair --server <server>`, and exit with `0`.
- R6. When `tervi disconnect` runs on a computer that is not paired, the
  worker shall print
  `This computer isn't paired. Pair it with: tervi pair --server <url>`,
  change nothing, and exit with `1`.
- R7. While the user has disconnected, `tervi status` shall show
  `○ Disconnected.` then `  Connect with: tervi connect`, and exit with `1`.
- R8. While the user has disconnected, the Machines page shall show the
  computer as `Offline · last seen <time ago>`, as for any computer that is
  not connected.
- R9. `tervi connect` after a disconnect shall connect again, as in slice 2's
  R4.

**Unpairing on the computer**

- R10. When the user runs `tervi unpair` on a paired computer, the worker
  shall print
  `This removes this computer's pairing with <server>.` then
  `tervi will stop running in the background. Continue? [y/N]` and wait.
  Only `y` or `yes` continues; anything else prints `Nothing was changed.`
  and exits with `1`. `--yes` skips the question.
- R11. When the user confirms, the worker shall first tell the server, which
  shall mark the machine unpaired and refuse its credential from then on.
  Then the worker shall stop tervi in the background, stop it from starting
  at login, remove its service file, delete the credential from the secret
  store, and delete `state.json`, print
  `✓ Unpaired. This computer is no longer paired with <server>.`, and exit
  with `0`. The `tervi` program shall stay installed.
- R12. If the server cannot be reached, then `tervi unpair` shall change
  nothing, print
  `✗ Can't reach <server>. Nothing was changed.` then
  `Try again later, or revoke this computer on the Machines page.`, and exit
  with `1`.
- R13. If the server already refuses this computer's credential (rejected or
  revoked), then `tervi unpair` shall treat the server part as done and
  remove the pairing on the computer as in R11.
- R14. When the user runs `tervi unpair --local-only`, the worker shall print
  `This removes this computer's pairing without telling <server>.` then
  `If that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]`,
  and on confirmation remove the pairing on the computer as in R11, without
  contacting the server, print `✓ Unpaired on this computer only.`, and exit
  with `0`. `--yes` skips the question.
- R15. When `tervi unpair` runs on a computer that is not paired, the worker
  shall print `This computer isn't paired. Nothing was changed.` and exit
  with `1`.
- R16. If removing something on the computer fails after the server was told
  (for example, the secret store cannot be reached), then the worker shall
  say what could not be removed and print `Finish with: tervi unpair --local-only`,
  and exit with `1`.

**Revoking on the Machines page**

- R17. Each machine on the Machines page shall have a **Revoke** button.
  Clicking it shall ask
  `Revoke <name>? It stops working at once and must be paired again to come back.`
  with **Revoke** and **Cancel**.
- R18. When the user confirms, the server shall mark the machine revoked,
  refuse its credential from then on, close its connection if it is
  connected, and stop listing it. The page shall stop showing it at once.
- R19. Unpaired and revoked machines shall not appear on the Machines page.
  The server shall keep their records.

**A computer that was unpaired on the server**

- R20. When the server refuses this computer's credential because it was
  revoked, the daemon shall stop trying, as in slice 2's R18, and write the
  log line
  `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server <server>`.
- R21. On such a computer, `tervi status`, `tervi connect`, and
  `tervi disconnect` shall print `✗ This computer was unpaired on the server.`
  then `Pair it again with: tervi pair --server <server>`. `tervi status` and
  `tervi connect` shall exit with `1`; `tervi disconnect` with `0`, as in R5.
- R22. On such a computer, `tervi pair` shall replace the old pairing, as
  slice 2's R33 does after a rejection, printing
  `This computer was unpaired on the server. Starting a new pairing.`

**Known limitation**

- R23. **Known limitation (as in slice 2's R27):** without sign-in, anyone
  who can reach the server can revoke any machine. The server still listens
  on `localhost` only.

## Not in this slice

*(to be written)*

## Open questions

- Recognizing a computer that pairs again.

## Decisions

- `tervi disconnect` pauses; it does not remove anything — like
  `tailscale down`; `tervi unpair` is the way to remove.
- The Machines page shows only `Offline · last seen …`, never why a
  computer is offline — the server cannot always learn why: the daemon's
  "disconnecting on purpose" message can be lost, and after a disconnect the
  daemon never connects again to repeat it, so a reason would sometimes be
  wrong. Tailscale's admin page does not show it either.
- `tervi disconnect` with nothing running still records the wish (R3) — so
  "disconnected" always means "stays off", and the next login cannot quietly
  connect again.
- `Already disconnected.` exits with `0` — the user's wish already holds;
  scripts should not fail on it, like `Already connected.` in slice 2.
- A rejected computer keeps its rejection message (R5) — `Already
  disconnected.` would hide that the fix is to pair again.
- `tervi unpair` asks before removing anything (R10) — it can only be undone
  by pairing again, which needs the browser and the code; `--yes` serves
  scripts.
- `tervi unpair` changes nothing when the server cannot be reached (R12) —
  the credential is the computer's only proof of who it is; deleting it
  would make "try again later" impossible.
- `tervi unpair --local-only` exists for a server that is gone for good (R14)
  — otherwise such a computer could never be cleaned up except by hand.
- A revoked computer is told plainly that it was unpaired on the server
  (R20, R21) — the server knows this for certain, unlike why a computer is
  offline; the computer learns it the next time it tries to connect, however
  long it was away, so nothing has to be delivered to it.
- Unpaired and revoked machines leave the Machines page but stay in the
  database (R19) — for history, and so a computer that pairs again can be
  recognized.
