# Task 02 — Server: unpair and Revoke, and the Machines page's Revoke button

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 01

## Goal

Add the two routes that end a machine's credential: the worker's
`POST /api/v1/machines/current/unpair`, proven with the credential, and the
Machines page's `POST /api/v1/machines/{machine_id}/revoke`, with no proof.
Both revoke the credential and close the machine's connection with `4002`.
The Machines page gets a Revoke button on each row, with its question.

## Requirements (copied from the spec)

- R11. When the user confirms, the worker shall first tell the server, which
  shall mark the machine unpaired and refuse its credential from then on.
  Then the worker shall stop tervi in the background, stop it from starting
  at boot, remove its service file, delete `credential.json`, and delete
  `state.json`, print
  `✓ Unpaired. This computer is no longer paired with <server>.`, and exit
  with `0`. The `tervi` program shall stay installed. Linger shall stay as
  it is; if it is on, the worker shall then print
  `  Linger is still on. Turn it off with: loginctl disable-linger`.
  *This task: the server's side, "tell the server, which shall mark the
  machine unpaired and refuse its credential from then on". The worker is
  task 06.*
- R12. If no answer comes from the server, then `tervi unpair` shall remove
  nothing on the computer, print
  `✗ No answer from <server>.` then
  `  The server may or may not have unpaired this computer. Nothing was removed here.`
  then `  Run tervi unpair again to finish, or revoke this computer on the Machines page.`,
  and exit with `1`. If the server answers with an error instead, then
  `tervi unpair` shall remove nothing on the computer, print
  `✗ <server> couldn't unpair this computer (HTTP <code>). Nothing was removed here.`
  then `  Run tervi unpair again to finish, or revoke this computer on the Machines page.`,
  and exit with `1`. Running `tervi unpair` again shall finish the unpairing,
  whether or not the server received the first request.
  *This task: repeating the unpair request after a lost answer answers
  `204` again, so running `tervi unpair` again finishes it.*
- R13. If the server already refuses this computer's credential (rejected or
  revoked), then `tervi unpair` shall treat the server part as done and
  remove the pairing on the computer as in R11.
  *This task: a credential the server does not know answers
  `401 unknown_credential`; an already revoked one answers `204`.*
- R17. Each machine on the Machines page shall have a **Revoke** button.
  Clicking it shall ask
  `Revoke <name>? It stops working at once and must be paired again to come back.`
  with **Revoke** and **Cancel**.
- R18. When the user confirms, the server shall mark the machine revoked,
  refuse its credential from then on, close its connection if it is
  connected, and stop listing it. The page shall stop showing it at once.
  If the request fails, the row shall stay and the page shall show
  `Couldn't revoke <name>. Try again.`
- R19. Unpaired and revoked machines shall not appear on the Machines page.
  The server shall keep their records.
- R30. **Known limitation (as in slice 2's R27):** without sign-in, anyone
  who can reach the server can revoke any machine. The server still listens
  on `localhost` only.

## Context

### What exists (task 01)

`internal/server/credential` has `Find(ctx, q, proof)` and
`Revoke(ctx, tx, machineID) (changed bool, err error)`, and the status
names. `connection.List` has `CloseRevoked(machineID)`. The machines list
shows only machines with an `active` credential. `httpapi` has
`BearerProof`, `WriteJSON`, `WriteError`, and `WriteInternalError`.

### Unpair: `internal/server/machines/unpair.go` (new)

`POST /api/v1/machines/current/unpair`, no body, proof
`Authorization: Bearer <credential>`. Registered by `machines.Register`.

| Credential found by `credential.Find` | Answer |
| --- | --- |
| `active` | In one transaction, `credential.RevokeCredential` on this credential; after commit, `List.CloseRevoked(machineID)` if it changed; `204` with no body |
| `revoked` | `204`, nothing changes (R12: repeating finishes) |
| no proof, no row, `pending`, `failed`, `expired` | `401 {"error": "unknown_credential"}` |
| a database failure | `500 internal_error`, logged with the kind `unpair machine` |

### Revoke: `internal/server/machines/revoke.go` (new)

`POST /api/v1/machines/{machine_id}/revoke`, no body, no proof (R30).

| Case | Answer |
| --- | --- |
| `machine_id` is not a UUID, or no machine has it | `404 {"error": "unknown_machine"}` |
| The machine has an `active` credential | In one transaction, `credential.Revoke`; after commit, `List.CloseRevoked(machineID)`; `204` |
| The machine has no `active` credential | `204`, nothing changes |
| A database failure | `500 internal_error`, kind `revoke machine` |

The route checks only that the machine exists; whether it had an `active`
credential comes from `credential.Revoke`'s `changed`, and the connection is
closed only when it changed (fix 1).

The route checks no `Origin` header and no proof: that was the user's
decision for this slice (plan, "Technical decisions"). The global `Host`
check still runs first, as for every route.

`unpair.go` and `revoke.go` share one function for "revoke this machine's
credential in a transaction, then close its connection".

### The Revoke button: `internal/server/web/static/`

Each row on the Machines page gets a **Revoke** button after its text.
Clicking it opens a question, using a `<dialog>` element written in
`machines.html` outside the part each refresh rebuilds:

```text
Revoke bert-desktop? It stops working at once and must be paired again to come back.
[ Revoke ]   [ Cancel ]
```

- The name is the row's `display_name`, inserted as text, never as HTML.
- **Cancel**, Escape, or closing the dialog changes nothing.
- **Revoke** sends `POST /api/v1/machines/<machine_id>/revoke`. On `204` or
  `404`, the row is removed from the page at once, without waiting for the
  next refresh (R18); a `404` means it is already gone.
- On any other answer, or no answer, the row stays and the failure line
  (the existing `aria-live` line) shows `Couldn't revoke <name>. Try again.`
  (R18) until a refresh sent after the failed answer finishes; after that
  the line follows the refresh as today (empty, or
  `Can't reach the server. Trying again…`).
- A refresh that arrives while the dialog is open does not close it. If the
  machine has disappeared from the list meanwhile, Revoke still sends the
  request, and the answer is handled as above. A failed answer never adds
  a machine back to the list.
- A refresh can already be waiting for its answer when a revoke answers.
  Its list was read before the revoke, so when it arrives:
  - machines revoked with `204` or `404` while it waited are dropped from
    it, so a removed row does not come back (R18);
  - if a revoke failed while it waited, the failure line stays, as above.

`machines-view.js` gets the logic as pure functions, tested with
`node --test` like the rest of the view, so `machines.js` only wires them to
the page:

```js
// The dialog's question for a machine.
export function revokeQuestion(machine) { … }
// The page state after a revoke answer: status is the HTTP status, or
// null when no answer came. 204 and 404 drop the machine from
// state.machines and clear state.revokeFailure; anything else leaves state.machines as it is and sets
// state.revokeFailure to the machine's name, which view() shows as the
// failure line.
export function afterRevoke(state, machineId, status) { … }
// The page state after a refresh answer. revokedSince holds the IDs of the
// machines revoked with 204 or 404 while that refresh waited; failedSince
// is true when a revoke failed while it waited. answerMachines (null when
// the refresh failed) replaces state.machines, without the machines in
// revokedSince. state.revokeFailure is cleared unless failedSince is true.
export function afterRefresh(state, answerMachines, revokedSince, failedSince) { … }
```

`view()` shows `Couldn't revoke <name>. Try again.` while
`state.revokeFailure` is set. `machines.js` keeps `revokedSince` and
`failedSince` for the refresh that is waiting, and resets them when it
sends the next refresh.

### Contract

Remove `x-planned: true` from `unpairMachine` and `revokeMachine` in
`api/openapi.yaml`. In `test/contract/contract_test.go`, call `unpairMachine`
(`204` twice, `401`, `403`) and `revokeMachine` (`204`, `404`, `403`).

## Boundaries

- May create or change: `internal/server/machines/` (its `Deps` already
  holds the connection list), `internal/server/web/` (Go and `static/`),
  their tests, `test/contract/contract_test.go`,
  `api/openapi.yaml` (only the two `x-planned` lines).
- Must not change: `internal/server/credential/`,
  `internal/server/connection/`, `internal/server/pairing/`,
  `internal/protocol/`, the migrations, anything under `internal/worker/`,
  `design/`, `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestUnpairRevokes` — an `active` credential answers `204`, becomes `revoked` with `ended_at`, and is refused on the connection route afterwards | R11 |
| `TestUnpairRepeat` — a second unpair with the same credential answers `204` and changes nothing | R12, R13 |
| `TestUnpairUnknown` — no proof, an unknown proof, and `pending`, `failed`, `expired` credentials answer `401 unknown_credential` | R13 |
| `TestUnpairClosesConnection` — a connected machine's connection is closed with `4002`, its credential is `revoked` by then, and it shows Offline | R11, R18 |
| `TestRevokeMachine` — a paired machine answers `204`, its credential is `revoked`, it disappears from `GET /api/v1/machines`, and its `machines` row is still there | R18, R19 |
| `TestRevokeClosesConnection` — a connected machine is closed with `4002` and is refused when it reconnects | R18 |
| `TestRevokeRepeatAndUnknown` — revoking again answers `204`; an unknown ID and a non-UUID answer `404 unknown_machine` | R18 |
| `TestRevokeNeedsNoProof` — the route works without any `Authorization` or `Origin` header | R30 |
| `TestRevokeDatabaseFailure` and `TestUnpairDatabaseFailure` — `500 internal_error`, with the kinds `revoke machine` and `unpair machine` in the log | R18, R11 |
| `machines-view.test.js` — the question text with a name, a name with `<b>` shown as text, the failure text | R17 |
| `machines-view.test.js` — `afterRevoke`: `204` and `404` drop the machine and set no failure; `500` and `null` leave the list unchanged (also when the machine is no longer in it) and set the failure line | R18 |
| `machines-view.test.js` — `afterRefresh`: the answer replaces the list without the machines in `revokedSince`, in the answer's order; the failure line is cleared, or kept when `failedSince` is true; a failed refresh (`null`) keeps the list | R18 |
| `TestMachinesPageHasRevokeDialog` — the page has the dialog outside the part that refreshes, with **Revoke** and **Cancel** | R17 |
| Contract test — `unpairMachine` and `revokeMachine` as above; no `x-planned` left on them | contract |

`go vet ./...`, `go test ./...`, and `node --test internal/server/web/static/`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- `tervi unpair` on the worker (task 06).
- What the worker does with `4002` (task 04).
- The approval page (task 03).

## If anything is unclear

Stop and report the question. Do not guess.
