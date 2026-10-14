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
- The Revoke route takes no proof and has no `Origin` check (R30): any
  request that reaches the server can revoke a machine whose `machine_id`
  it names — the simplest route while there is no sign-in; the
  `machine_id` is random, and another website cannot read the Machines list
  to learn it. Sign-in comes before release, in a later slice.
  Not chosen: accepting only requests whose `Origin` equals the address the
  request was sent to — blocks a revoke sent by another website open in the
  user's browser, but the user chose to keep this route as simple as
  possible until sign-in; a token the page sends back — more code for the
  same protection as the `Origin` check.
- When a connected machine is revoked, the server closes its connection
  with a new close code, `4002` ("revoked") — the worker learns at once
  that it must stop, instead of reconnecting once only to be refused.
  Not chosen: an existing code such as `1001` — it would say something
  untrue ("the server is stopping"), and the worker would try once more
  before stopping.
- The server alone decides why a credential is refused, from that
  credential's one row (found by its unique hash): `revoked` answers
  `401 {"error": "credential_revoked"}`; no row, or any other status that
  is not `active`, answers `401 {"error": "unknown_credential"}` as in
  slice 0002. `credential_revoked` and the close code `4002` mean the same,
  so a computer that was offline when it was revoked learns the same reason
  on its next try as one that was connected (R20, R21).
  Not chosen: one answer, `unknown_credential`, for every refused
  credential — a revoked computer that was offline would show slice 0002's
  message while a connected one, closed with `4002`, would show R20's;
  dropping `4002` too and showing slice 0002's message in every case —
  consistent and simpler, but the computer could no longer say what
  happened, and R20–R22 would change.
- A credential that stopped working has one status, `revoked`, whether
  `tervi unpair` or the Revoke button ended it — the result on the server
  is the same, so one value represents it; the two differ only in what the
  computer does, which the server does not record.
  Not chosen: separate `unpaired` and `revoked` statuses — two values for
  the same state, kept only to show on the approval page which way a
  pairing ended.
