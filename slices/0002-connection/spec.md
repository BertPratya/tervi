# 0002 Connection — slice 2

Status: draft

## What the user does

1. On the paired computer, the user starts the daemon in a terminal and
   leaves it open:

   ```text
   $ tervi daemon
   tervi daemon started for http://localhost:8080 (bert-desktop)
   10:42:01 ✓ Connected
   ```

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

4. The user stops the server. Within a few seconds the daemon shows that it is
   reconnecting, and keeps trying:

   ```text
   10:55:13 ✗ Connection lost: the server closed the connection
   10:55:14 … Reconnecting (try 1)
   10:55:14 ✗ Connection refused. Next try in 2 seconds
   ```

5. The user starts the server again. The daemon reconnects by itself, and the
   Machines page shows **Online** again.

6. The user stops the daemon with Ctrl+C. The Machines page shows
   `Offline · last seen 1 minute ago`.

7. Pairing a new computer while its daemon runs connects it at once:

   ```text
   $ tervi pair --server http://localhost:8080
   ...
   ✓ Paired successfully.
   ✓ Connected.
   ```

## Whole design (all slices, short)

The worker runs a **daemon**: a long-running background program that keeps
one connection to the server open. Short commands (`tervi pair`,
`tervi connect`, `tervi status`) talk to the daemon on the same computer; they
never hold the connection themselves. The server can only be reached by the
worker calling it, so the worker always opens the connection, never the
server.

```text
tervi pair ────┐
tervi connect ─┼── same computer ──►  tervi daemon  ── connection ──►  server
tervi status ──┘                      reads "connect"                 Online / Offline
```

`state.json` holds `connect`: whether this computer wants to be connected.
The daemon obeys it at all times.

| Slice | Adds |
| --- | --- |
| 0002 (this one) | The daemon, started by hand; the connection; Online/Offline; `tervi status` and `tervi connect` |
| 0003 | The daemon starts by itself when the user logs in (a systemd user service) |
| Later | `tervi disconnect`, unpairing, revoking a machine from the server, running agents over this connection |

## Must always be true (this slice)

**Wanting to connect**

- R1. When pairing succeeds, the worker shall set `connect` to true in
  `state.json`.
- R2. While `connect` is true, the daemon shall stay connected to the server,
  and reconnect whenever the connection is lost, with no limit on the number
  of tries.
- R3. While `connect` is false, the daemon shall not try to connect.
- R4. When the user runs `tervi connect`, the worker shall set `connect` to
  true and the daemon shall connect. If it is already connected, the worker
  shall print `Already connected.` and change nothing. If no daemon runs, the
  worker shall set `connect` to true and print
  `The tervi daemon isn't running. Start it with: tervi daemon`.

**The daemon**

- R5. The user shall start the daemon with `tervi daemon`. It shall run until
  the user presses Ctrl+C, then print `Stopped.` and leave `connect` as it is.
- R6. If a daemon is already running on the computer, then `tervi daemon`
  shall print `tervi daemon is already running.` and stop.
- R7. If the computer is not paired when the daemon starts, then the daemon
  shall print `This computer isn't paired yet.` and
  `Waiting for pairing. Run: tervi pair --server <url>`, keep running, and
  connect as soon as pairing succeeds.
- R8. The daemon shall print one line, with the time, each time the connection
  changes (connected, lost with the reason, each reconnect try with the reason
  it failed and the wait before the next), and nothing for the regular checks.
- R9. Between reconnect tries, the daemon shall wait 1, 2, 4, 8 ... seconds,
  at most 30 seconds, then keep trying every 30 seconds.

**Pairing connects**

- R10. When pairing succeeds while the daemon runs, the worker shall tell the
  daemon, and the terminal shall show `✓ Paired successfully.` then
  `✓ Connected.` once connected.
- R11. When pairing succeeds while no daemon runs, the terminal shall show
  `✓ Paired successfully.` then
  `The tervi daemon isn't running. Start it with: tervi daemon`.

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
  - `✗ The tervi daemon isn't running.` with `Start it with: tervi daemon`;
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

**Machines page**

- R21. The server shall show `/machines`: every paired machine (only those
  whose pairing finished), with its name, OS, and `Online` or `Offline`. An
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

## Not in this slice

- Starting the daemon at login (slice 3).
- `tervi disconnect`, unpairing, and revoking a machine. Nothing in this slice
  sets `connect` to false except a rejected credential (R18).
- Sending any work over the connection: agents, files, terminals.
- Sign-in, HTTPS, Windows, macOS.
- Renaming machines.

## Open questions

None.

## Decisions

- A daemon holds the connection, and short commands talk to it — the same
  design as Tailscale (`tailscaled` and `tailscale`); a short command cannot
  keep a connection open after it exits.
- The daemon is started by hand in this slice, and by systemd in slice 3 —
  each slice stays small; only who starts the daemon changes.
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
- Online is not stored in the database — it is true exactly while a
  connection is open; storing it would go stale whenever the server stops.
- The Machines page is plain HTML and JavaScript, like the approval page — the
  Theia interface replaces it later.
