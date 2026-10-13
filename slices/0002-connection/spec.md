# 0002 Connection — slice 2

Status: approved

## What the user does

1. The user installs tervi and pairs the computer. Pairing also sets tervi up
   to run in the background, and it connects at once:

   ```text
   $ go install ./cmd/tervi
   $ tervi pair --server http://localhost:8080
   ...
   ✓ Paired successfully.
   ✓ tervi now runs in the background and starts when you log in.
   ✓ Connected.
   $
   ```

   `tervi pair` ends and the prompt comes back. The user can keep using the
   terminal or close it; tervi stays connected in the background.

2. The user opens `http://localhost:8080/machines` in a browser:

   ```text
   Machines
   ● bert-desktop   Ubuntu 26.04   Online
   ```

3. In another terminal, the user checks the connection:

   ```text
   $ tervi status
   ● Connected to http://localhost:8080
     Machine: bert-desktop
     Since:   10:42 (12 minutes)
   ```

4. The user stops the server. Within a few seconds `tervi status` shows that
   tervi is reconnecting, and the daemon's log shows each try:

   ```text
   $ tervi status
   ○ Reconnecting to http://localhost:8080
     Try 3, next in 4 seconds
     Last error: connection refused

   $ tervi logs
   10:55:13 ✗ Connection lost: the server is stopping
   10:55:14 … Reconnecting (try 1)
   10:55:14 ✗ Connection refused. Next try in 2 seconds
   ```

5. The user starts the server again. The daemon reconnects by itself, and the
   Machines page shows **Online** again.

6. The user restarts the computer and logs in. Without typing anything, the
   Machines page shows **Online** again within a few seconds.

7. The user stops the daemon with `systemctl --user stop tervi`. The Machines
   page shows `Offline · last seen 1 minute ago`. `tervi connect` starts it
   again.

## Whole design (all slices, short)

The worker runs a **daemon**: a long-running background program that keeps
one connection to the server open. systemd starts it when the user logs in, as
a user service named `tervi`. The user never needs to start it by hand, but
can, with `tervi daemon`. Short commands
(`tervi pair`, `tervi connect`, `tervi status`) talk to the daemon on the same
computer; they never hold the connection themselves. The server can only be reached by the
worker calling it, so the worker always opens the connection, never the
server.

```text
tervi pair ────┐
tervi connect ─┼── same computer ──►  daemon        ── connection ──►  server
tervi status ──┘                      reads "connect"                 Online / Offline
                                      started by systemd at login
```

`state.json` holds `connect`: whether this computer wants to be connected.
The daemon obeys it at all times.

| Slice | Adds |
| --- | --- |
| 0002 (this one) | The daemon as a systemd user service that starts at login; the connection; Online/Offline; `tervi status`, `tervi connect`, and `tervi logs` |
| 0003 | `tervi disconnect`, and unpairing (which also removes the service) |
| Later | Revoking a machine from the server, running agents over this connection |

## Must always be true (this slice)

**Wanting to connect**

- R1. When pairing succeeds, the worker shall set `connect` to true in
  `state.json`.
- R2. While `connect` is true, the daemon shall stay connected to the server,
  and reconnect whenever the connection is lost, with no limit on the number
  of tries.
- R3. While `connect` is false, the daemon shall keep running but not try to
  connect. It shall still answer `tervi status` and `tervi connect`.
- R4. When the user runs `tervi connect` on a paired computer, the worker shall
  set `connect` to true, set up and start the background service if it is
  missing or stopped (as in R5), and print `✓ Connected.` once connected. If
  it is already connected, the worker shall print `Already connected.` and
  change nothing. On a computer that is not paired, it shall print
  `This computer isn't paired. Pair it with: tervi pair --server <url>` and
  change nothing. When it is not connected:
  - If the daemon has not connected within 30 seconds, `tervi connect` shall
    print `Not connected yet: <last error>. tervi keeps trying in the background.`
    and `Check with: tervi status`. The daemon keeps trying (R2).
  - If the server rejects the credential (R20), it shall print
    `✗ The server doesn't accept this computer's credential.` and
    `Pair again with: tervi pair --server <server>`.
  - If the background service cannot be set up or started, it shall say so
    with the reason and print `Try again with: tervi connect`.

  `tervi connect` shall exit with `0` when connected or already connected,
  and `1` otherwise.

**The daemon**

- R5. When pairing succeeds, the worker shall set up the daemon as a systemd
  user service named `tervi`, start it, and enable it so that it starts every
  time the user logs in. The service shall run the same `tervi` program file
  that set it up.
- R6. If the daemon stops unexpectedly, systemd shall start it again after a
  few seconds.
- R7. At most one daemon shall run per user account. A second one shall exit
  at once and leave the first one running.
- R8. The daemon shall write one log line, with the time, each time the
  connection changes (connected, lost with the reason, each reconnect try with
  the reason it failed and the wait before the next), and nothing for the
  regular checks. The log is kept by systemd's journal.
- R32. When the user runs `tervi logs`, the worker shall print the
  background daemon's log so far and exit. With `-f`, it shall then keep
  printing new lines as they are written, until the user presses Ctrl+C.
  The log of a daemon run by hand with `tervi daemon` is printed in its own
  terminal instead (R30).
- R28. If `tervi pair` or `tervi connect` runs from a temporary program file
  (as `go run` creates), then it shall print
  `tervi must be installed to run in the background. Install it with: go install ./cmd/tervi`
  and stop. `tervi pair` shall do this before contacting the server.
- R29. If the background service cannot be set up or started (for example,
  systemd is not available), then the worker shall say so with the reason,
  keep the pairing, and print `Try again with: tervi connect`.
- R30. When the user runs `tervi daemon`, the worker shall run the daemon in
  that terminal and print its log lines there, until the user presses Ctrl+C.
  If a daemon is already running for this user account, it shall print
  `tervi is already running in the background.` and exit.
- R9. Between reconnect tries, the daemon shall wait about 1, 2, 4, 8 ...
  seconds, at most about 30 seconds, then keep trying about every 30
  seconds. Each wait shall be that value, made longer or shorter at random by
  up to 20%, so that workers that lost the server at the same moment do not
  all try again at the same moment.

**Pairing connects**

- R10. When pairing succeeds, the terminal shall show `✓ Paired successfully.`,
  then `✓ tervi now runs in the background and starts when you log in.`, then
  `✓ Connected.` once the daemon is connected.
- R11. If the daemon has not connected within 30 seconds after pairing, then
  the terminal shall say that pairing is done but the connection is still
  being tried, show the last error, and print `Check with: tervi status`.
- R31. When `tervi pair` or `tervi connect` has printed its last line, it
  shall exit and give the prompt back, leaving the daemon running in the
  background. Closing that terminal, or running other commands in it, shall
  not stop the daemon or its connection. The user works with the running
  daemon through `tervi` commands, such as `tervi status` and
  `tervi connect`.

**Noticing the other side is gone**

- R12. When either side closes the connection on purpose, the other side shall
  notice at once.
- R13. If the other side stops answering without closing (for example, the
  network is lost), then each side shall notice within the timeout: 30
  seconds by default.
- R14. The check interval (default 10 seconds) and the timeout (default 30
  seconds) shall be set on the server only. The server shall tell each worker
  the values when it connects, so both sides always use the same ones.

**One connection per machine**

- R15. The server shall keep at most one connection per machine. When a new
  connection arrives for a machine that already has one, the server shall
  close the old one, keep the new one, and log a warning naming the machine.

**Status**

- R16. `tervi status` shall show one of:
  - `● Connected to <server>`, with the machine name and since when;
  - `○ Reconnecting to <server>`, with the try number, the wait before the
    next try, and the last error;
  - `✗ tervi isn't running in the background.` with `Start it with: tervi connect`;
  - `✗ This computer isn't paired.` with `Pair it with: tervi pair --server <url>`;
  - `✗ Not connecting: the server doesn't accept this computer's credential.`
    with `Pair again with: tervi pair --server <server>`.
- R17. `tervi status` shall exit with `0` when connected, and `1` otherwise.

**Rejected credential**

- R18. If the server answers that it does not know this computer's credential,
  then the daemon shall stop trying, set `connect` to false, save the reason in
  `state.json`, and print that the credential was rejected and how to pair
  again.
- R19. The daemon shall treat only that answer as a rejection. A refused
  connection, a timeout, a network error, or a server error shall lead to
  another try, never to `connect` false.
- R20. When `tervi connect` runs after a rejection, the daemon shall try once.
  If the credential is rejected again, it shall stop as in R18.
- R33. When `tervi pair` runs on a computer whose credential was rejected
  (R18), the worker shall print
  `The server no longer accepts this computer's credential. Starting a new pairing.`,
  remove the old pairing (the secret store entry and the pairing in
  `state.json`), and start a new pairing. On a paired computer whose
  credential was not rejected, `tervi pair` shall still refuse, as in slice 1
  (R2 of slice 1).

**Machines page**

- R21. The server shall show `/machines`: every paired machine (only those
  whose pairing finished), sorted by name, with its name, OS, and `Online` or
  `Offline`. An
  offline machine shall also show `last seen <time ago>`, or `never connected`
  if it never connected.
- R22. The page shall refresh by itself every 5 seconds.
- R23. A machine shall show as Online exactly while the server has its
  connection open. After the server restarts, every machine shall show as
  Offline until it connects again.
- R24. The server shall record when it last heard from each machine: when it
  connects, at least once a minute while connected, and when the connection
  ends (the time of the last message received). "Last seen" may be up to one
  minute early if the server itself stopped suddenly.

**Secrets**

- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`.
- R26. Only the user's own account shall be able to talk to the daemon.
- R27. **Known limitation (as in slice 1):** without sign-in, anyone who can
  reach the server can see the Machines page. The server still listens on
  `localhost` only.

**Server log**

- R34. When the server answers `internal_error`, it shall log what kind of
  operation failed, and never the error's details, a request value, a key,
  a credential, or a hash.

## Messages

The exact wording of messages the requirements above describe but do not
quote. `<…>` is filled in.

**`tervi pair` and `tervi connect`**

| When | Message |
| --- | --- |
| Paired, but not connected within 30 seconds (R11) | `Paired, but not connected yet: <last error>. tervi keeps trying in the background.` then `Check with: tervi status` |
| The background service cannot start (R29) | `✗ Can't start tervi in the background: <reason>` then `Try again with: tervi connect` |
| Ctrl+C while connecting after pairing | `Pairing finished. Connecting was cancelled; tervi keeps trying in the background.` |
| The background service started, but asking it to connect failed | `✗ Can't reach tervi in the background: <reason>` then `Check with: tervi status` (exit `1`) |
| Ctrl+C while `tervi connect` waits for the connection | `Connecting was cancelled; tervi keeps trying in the background.` (exit `130`; Ctrl+C stops only the waiting, never the daemon) |

**`tervi status`** (R16)

| When | Message |
| --- | --- |
| Connected | `● Connected to <server>`, `  Machine: <hostname>`, `  Since:   <HH:MM> (<how long>)` |
| Reconnecting | `○ Reconnecting to <server>`, `  Try <n>, next in <s> seconds`, `  Last error: <error>` |
| Reconnecting, a try happening now | `  Try <n>, trying now` in place of the `next in` line |
| Reconnecting, no try has failed yet | The `Last error:` line is left out |
| Running, not connecting, no rejection (a slice-1 pairing) | `○ Not connecting.` then `  Connect with: tervi connect` |

`<how long>` is `less than a minute`, `1 minute`, `N minutes`, `1 hour`,
`N hours`, `1 day`, or `N days`.

**The daemon's log** (R8). Each line starts with the time, `HH:MM:SS`.

| When | Line |
| --- | --- |
| Connected | `✓ Connected to <server>` |
| Lost | `✗ Connection lost: <reason>`, where `<reason>` is `the server is stopping`, `replaced by a newer connection`, `no answer from the server for <timeout> seconds`, `the server closed the connection`, or the network error |
| A try starts | `… Reconnecting (try <n>)` |
| A try failed | `✗ <error>. Next try in <s> seconds` |
| Rejected (R18) | `✗ The server doesn't accept this computer's credential. Pair again with: tervi pair --server <server>` |

**The Machines page** (R21)

| When | Text |
| --- | --- |
| No machines | `No machines yet. Pair one with: tervi pair --server <this page's address>` |
| The server cannot be reached | `Can't reach the server. Trying again…` above the last list shown |
| `<time ago>` | `less than a minute ago`, `1 minute ago`, `N minutes ago`, `1 hour ago`, `N hours ago`, `1 day ago`, `N days ago` |

**Rare errors**

| When | Message |
| --- | --- |
| `tervi daemon` without a desktop session's runtime folder | `✗ Can't find this session's runtime folder (XDG_RUNTIME_DIR is not set).` |
| `tervi logs` cannot read the log | `✗ Can't read the log: <reason>` |
| tervi was built without one of its internal settings | `✗ tervi is missing an internal setting (<field name>).` |

## Not in this slice

- `tervi disconnect`, unpairing, removing the background service, and revoking
  a machine. Nothing in this slice sets `connect` to false except a rejected
  credential (R18).
- Running in the background without logging in (systemd "linger"). Starting
  by itself on Linux systems without systemd; there, the user runs
  `tervi daemon`.
- Sending any work over the connection: agents, files, terminals.
- Sign-in, HTTPS, Windows, macOS.
- Renaming machines.

## Open questions

None.

## Decisions

- A daemon holds the connection, and short commands talk to it — the same
  design as Tailscale (`tailscaled` and `tailscale`); a short command cannot
  keep a connection open after it exits.
- The daemon always runs by default: pairing sets it up as a systemd user
  service that starts at login — the user wants it always on, like
  `tailscaled`; starting it on demand would leave the computer offline after
  every reboot until a command ran. `tervi daemon` also runs it by hand, like
  `tailscaled` can be — for watching the log, or on a computer without
  systemd.
- A user service, not a system service — the credential is in the user's
  GNOME Keyring, which unlocks only when the user logs in.
- tervi must be installed (`go install`) before pairing — the service needs a
  program file that stays in place; `go run` deletes its file when it exits.
- The daemon keeps running while `connect` is false — like `tailscaled` after
  `tailscale down`; exiting would make systemd restart it, and it would start
  and exit again at every login.
- `connect` in `state.json` is the source of truth for whether to connect —
  it survives restarts, and a later `tervi disconnect` only has to set it.
- Newest connection wins — after a network drop, the worker reconnects before
  the server notices the old connection is dead; refusing the new one would
  keep the machine offline for up to 30 seconds.
- The server sets the check timing — one place to change it, and both sides
  always agree.
- A rejected credential stops retrying and sets `connect` to false, but only
  on a clear "unknown credential" answer — retrying can never succeed, and
  treating temporary errors the same way could switch off every worker after
  one bad server deploy.
- `tervi pair` may replace a pairing only after the server rejected its
  credential (R33) — otherwise the advice to pair again would be refused;
  the old credential is already worthless, so nothing of value is lost.
  General unpairing comes in slice 0003.
- Online is not stored in the database — it is true exactly while a
  connection is open; storing it would go stale whenever the server stops.
- The Machines page is plain HTML and JavaScript, like the approval page — the
  Theia interface replaces it later.
