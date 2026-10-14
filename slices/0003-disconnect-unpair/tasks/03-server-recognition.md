# Task 03 — Server: recognizing a computer that pairs again, and the approval page

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 01

## Goal

When a pairing starts with an identity, the server finds the earlier
machine with that identity, stores the match on the pairing request, and
the approval page shows it with Accept and Reject. Accepting brings that
machine back: the new credential is attached to it, and when the worker
acknowledges, the machine's earlier credential is revoked and its name and
OS are updated.

## Requirements (copied from the spec)

- R23. When a computer starts pairing, the worker shall send the server an
  identity of this computer that tervi derives from the operating system's
  machine ID. The machine ID itself shall never leave the computer, and the
  identity shall never be shown on any page or printed.
  If the worker cannot read the machine ID, then `tervi pair` shall first
  print `! tervi can't read this computer's machine ID (/etc/machine-id).`,
  then `  Without it, the server won't recognize this computer if you pair it`,
  `  again later: it will come back as a new computer, without its history.`,
  `  To fix this, create a machine ID first: sudo systemd-machine-id-setup`,
  then `Pair without it? [y/N]` and wait. Only `y` or `yes` continues (as in
  R10), and
  the pairing then sends no identity, so the server never matches this
  machine (R28); anything else prints `Nothing was changed.` and exits with
  `1`. `--yes` skips the question.
  *This task: the server's side: accepting, checking, and storing
  `identity_hash`, never showing or logging it. Computing and sending it is
  task 07.*
- R24. When the identity matches an earlier machine, the approval page shall
  show `This looks like a computer that was paired before:` and
  `<earlier name>, unpaired on <date>.`, or
  `<earlier name>, still paired.` for a machine that is still paired. It
  shall offer two buttons, each with its result next to it:
  **Accept** (`brings back <earlier name>, with its history`) and
  **Reject** (`doesn't pair it`).
- R25. A match shall be found by the identity alone, also when the computer's
  name changed. When several earlier machines match, the page shall offer the
  most recently paired one.
- R26. When the user clicks **Accept** on such a page and the pairing
  completes, the server shall continue the earlier machine instead of
  creating a new one: the same entry, keeping its history, now with the
  computer's current name. If the earlier machine's credential still worked,
  it shall stop working.
- R27. When the identity matches an earlier machine, the computer shall not
  be paired as a new machine: it comes back as the earlier one (R26), or the
  user rejects it.
- R28. When the identity matches no earlier machine, the approval page shall
  be exactly as in slice 1.
- R29. **Known limitation:** the identity is the computer's claim, which the
  server cannot verify; a computer that knew another's identity could claim
  to be it. This is why the server only offers the match and the user
  decides. After the operating system is reinstalled the computer is not
  recognized, and virtual machines copied from one image may look like the
  same computer; to pair such a copy as a computer of its own, give it a new
  machine ID first (delete `/etc/machine-id`, then run
  `systemd-machine-id-setup`).
  *This task: the server only offers the match it stored; nothing is
  merged without Accept.*

## Context

### What exists (task 01)

`machines.identity_hash`, `pairing_requests.identity_hash`, and
`pairing_requests.matched_machine_id` exist (all null so far).
`internal/server/credential` has `Find`, `Revoke`, and the status names;
`connection.List` has `CloseRevoked(machineID)`. `issueCredential` inserts a
`machines` row, then a `pending` `credentials` row.

### Start: `internal/server/pairing/start.go`

`POST /api/v1/pairings` accepts an optional string field `identity_hash`:
exactly 64 lowercase hex characters (`^[0-9a-f]{64}$`). Any other value,
including a non-string, answers `400 invalid_input`. It is decoded to 32
bytes and stored in `pairing_requests.identity_hash`. Never log it or put
it in an error message.

In the same transaction as the insert, if `identity_hash` is set, the
server looks for a match (next part) and stores it in
`matched_machine_id`. The match is decided only here, once.

### Matching: `internal/server/pairing/match.go` (new)

A machine matches when `machines.identity_hash` equals the request's and the
machine has at least one credential that is `active` or `revoked` (it was
paired at least once). When several match, take the machine whose most
recent credential (by `credentials.created_at`, then `credential_id`) was
created last (R25). A null identity never matches anything. One query.

### The approval answer: `internal/server/pairing/approval.go`

`readApproval`, `accept`, and `reject` all answer `Approval`; when the
request has a `matched_machine_id`, the answer includes `earlier_machine`
(`api/openapi.yaml`, `EarlierMachine`):

| Field | Value |
| --- | --- |
| `display_name` | The matched machine's `display_name` |
| `still_paired` | `true` while it has an `active` credential |
| `unpaired_at` | `null` while `still_paired`; otherwise the `ended_at` of its most recently ended credential, as RFC 3339 in UTC |

Read live each time, so the page shows the machine's state now. With no
match, the answer is exactly as in slice 1 (R28).

### The correct code: `internal/server/pairing/code.go`

`issueCredential`: when the request has a `matched_machine_id`, insert no
`machines` row; attach the new `pending` credential to the matched machine
and answer that machine's ID as `machine_id`. Otherwise, as in task 01, and
the new machine gets the request's `identity_hash`. The earlier credential
is not touched here: it keeps working until the acknowledgment (R26).

### The acknowledgment: `internal/server/pairing/machine.go`

In the transaction that makes the new credential `active` (and the pairing
request `paired`), when the request has a `matched_machine_id`:

1. `credential.Revoke` the machine's earlier `active` credential, before
   activating the new one (the database allows one `active` per machine).
2. Update the machine's `hostname`, `os_name`, `os_version` from the pairing
   request, and `display_name` as for a new machine (the hostname, or
   `unknown`).
3. After commit, if a credential was revoked, `List.CloseRevoked(machineID)`.

`pairing.Deps` gets the connection list for step 3; wire it in
`internal/server/server.go`.

A matched machine with no `active` credential (unpaired earlier) simply
gets its new credential. Save-failure and expiry leave the matched machine
and its earlier credential untouched.

### The approval page: `internal/server/web/static/`

When `earlier_machine` is present and the status is
`waiting_for_approval`, the page shows, below the computer's name and OS:

```text
This looks like a computer that was paired before:
bert-desktop, unpaired on 13 October.

[ Accept ]   brings back bert-desktop, with its history
[ Reject ]   doesn't pair it
```

- Line 2 is `<display_name>, unpaired on <date>.`, or
  `<display_name>, still paired.` when `still_paired`.
- `<date>` is `unpaired_at` in the browser's time zone, written as day and
  month name (`13 October`), with the year added when it is not the current
  year (`13 October 2025`).
- Each button has its result next to it, as shown. With no
  `earlier_machine`, the page is exactly as in slice 1, buttons without
  results.
- Names are inserted as text, never as HTML.

Put the text in `view.js` as pure functions and test it with `node --test`.

## Boundaries

- May create or change: `internal/server/pairing/`, `internal/server/web/`
  (Go and `static/`), `internal/server/server.go` (wiring the list into
  pairing), their tests, `test/contract/contract_test.go`.
- Must not change: `internal/server/credential/`,
  `internal/server/connection/`, `internal/server/machines/`, the
  migrations, `api/`, anything under `internal/worker/`, `design/`,
  `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestStartStoresIdentity` — a valid `identity_hash` is stored as 32 bytes; none stores null | R23 |
| `TestStartRejectsBadIdentity` — uppercase, 63 or 65 characters, non-hex, and a number answer `400 invalid_input`; the value is not in the log | R23 |
| `TestMatchPairedBefore` — a machine with a `revoked` or `active` credential and the same identity matches; one whose only credentials `failed` or `expired` does not; a null identity matches nothing | R24, R28 |
| `TestMatchMostRecent` — of two matching machines, the one whose latest credential is newer is chosen, also when its name differs | R25 |
| `TestMatchDecidedOnce` — a machine that starts matching after the pairing started does not change the stored match | R24 |
| `TestApprovalShowsEarlierMachine` — `earlier_machine` with `still_paired: false` and `unpaired_at`, or `still_paired: true` and `null`; absent with no match | R24, R28 |
| `TestCodeAttachesToMatchedMachine` — the correct code creates no machine, attaches the `pending` credential to the matched machine, answers its ID; the earlier `active` credential still works | R26, R27 |
| `TestAcknowledgeBringsMachineBack` — after the acknowledgment the new credential is `active`, the earlier one `revoked`, the machine's name and OS are the new ones, and it is listed once | R26 |
| `TestAcknowledgeClosesEarlierConnection` — an earlier credential's open connection is closed with `4002` after commit | R26 |
| `TestSaveFailureKeepsEarlierCredential` — a reported save failure or an expired credential leaves the matched machine and its earlier credential as they were | R26 |
| `view.test.js` — the match lines for unpaired and still paired, the date with and without the year, the button results, and the page without a match unchanged | R24, R28 |
| Contract test — `startPairing` with `identity_hash`; `readApproval` with `earlier_machine` | contract |

`go vet ./...`, `go test ./...`, and `node --test internal/server/web/static/`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- Computing and sending the identity, and the machine ID question
  (task 07).
- The unpair and Revoke routes (task 02).

## If anything is unclear

Stop and report the question. Do not guess.
