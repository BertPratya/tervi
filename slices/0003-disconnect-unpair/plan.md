# 0003 Disconnect and unpair — plan

Status: draft
Spec: [spec.md](spec.md)

The rest of this plan is written with the user after slice 0002 is built,
because it depends on 0002's code. Design decisions that do not depend on it
are recorded here as they are made.

## Technical decisions

- A computer's identity for recognizing it (R23) is a one-way hash of
  `/etc/machine-id` combined with a fixed tervi-specific value, so the same
  computer always gives the same identity, and the machine ID itself can't
  be worked back from it. It is computed as systemd does for an
  application-specific ID: HMAC-SHA256 keyed with the machine ID, over
  tervi's fixed 16-byte app ID, a constant in the worker's code that never
  changes. The worker computes it each time it pairs, sends only the
  result, and saves nothing; the server only stores and compares it, on
  each machine — it survives a
  rename and the loss of tervi's own files, and systemd's documentation asks
  applications to use an application-specific value derived from the
  machine ID instead of the confidential ID itself.
  Not chosen: a random ID that tervi creates and keeps in a file — lost
  whenever tervi's files are, which is the case recognition is most needed
  for; the raw `/etc/machine-id` — systemd treats it as confidential, and
  sending it lets other software link the computer across services.
- Only the program holding `$XDG_RUNTIME_DIR/tervi/daemon.lock` writes
  `connect` and `stop_reason` in `state.json`. A running daemon holds it, so
  `tervi disconnect` first tries to take the lock: if it gets it, no daemon
  is running and none can start, so it writes `connect: false` itself and
  lets go; if it cannot, it asks the running daemon (R1, R3) — there is
  still one writer at a time, and R3 forbids starting the daemon just to
  write. This replaces slice 0002's "only the daemon writes".
  Not chosen: starting the daemon to write it, then stopping it — breaks
  R3; a separate marker file such as `~/.config/tervi/disconnected` — two
  files would answer "should I connect?" and could disagree.
- `connect: false` with no `stop_reason` means the user disconnected; no
  new value is added. A computer paired in slice 0001 that never ran
  `tervi connect` is in the same state and also shows `○ Disconnected.`
  (R7) — that message is true for it too, and one state is less to keep
  correct.
  Not chosen: a new `stop_reason: "disconnected"` — keeps slice 0002's
  `○ Not connecting.` for that rare leftover case, at the cost of a third
  state.
- The server keeps the computer and its credentials apart: `machines` holds
  one row per computer (name, operating system, `last_seen_at`, identity),
  and a new table holds one row per pairing that issued a credential (its
  hash, status, expiry, and when it ended). Pairing again adds a new
  credential row; no credential goes back to `active` — while "Accept as the
  same computer" is finishing, one computer has its old `active` credential
  and its new `pending` one at the same time (R26), which one row cannot
  hold, and later slices' workspaces can point at the computer alone.
  Not chosen: one table, where "same computer" creates a new row pointing to
  the old one — the computer's history is spread over a chain of rows that
  every later feature must follow; one table updated in place with a second
  credential column — brings back `revoked → active`, and every check must
  look at two credential columns.
- Whether a machine is paired is not stored on `machines`: a machine is
  paired exactly while it has an `active` credential, and at most one
  credential per machine can be `active` — the fact lives in one place, so
  the two tables can never disagree.
  Not chosen: a `status` column on `machines` as well — every unpair,
  revoke, and pairing again would have to update both tables, and a missed
  update could list a computer whose credential no longer works.
