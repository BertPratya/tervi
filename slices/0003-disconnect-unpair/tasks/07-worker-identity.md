# Task 07 — Worker: the computer's identity when pairing

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 03, 04, 06

## Goal

`tervi pair` computes the computer's identity from `/etc/machine-id` and
sends it when the pairing starts, so the server can recognize a computer
that pairs again. Without a readable machine ID, it asks first. On a
computer whose credential was revoked, `tervi pair` starts a new pairing,
as slice 2 does after a rejection.

## Requirements (copied from the spec)

- R22. On such a computer, `tervi pair` shall replace the old pairing, as
  slice 2's R33 does after a rejection, printing
  `This computer was unpaired on the server. Starting a new pairing.`
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
  *This task: computing and sending the identity, and the question. The
  server's side is task 03.*
- R29. **Known limitation:** the identity is the computer's claim, which the
  server cannot verify; a computer that knew another's identity could claim
  to be it. This is why the server only offers the match and the user
  decides. After the operating system is reinstalled the computer is not
  recognized, and virtual machines copied from one image may look like the
  same computer; to pair such a copy as a computer of its own, give it a new
  machine ID first (delete `/etc/machine-id`, then run
  `systemd-machine-id-setup`). A computer paired before this slice has no
  identity on the server, so the first time it pairs again it is not
  recognized.
  *This task: the identity comes only from the machine ID, so a reinstalled
  system gives a new one and copied systems give the same one.*

## Context

### What exists (tasks 03 and 04, slices 1 and 2)

- The server accepts an optional `identity_hash` in `POST /api/v1/pairings`
  (64 lowercase hex characters) and answers `400 invalid_input` for
  anything else (task 03).
- `state.json` can hold `stop_reason: "credential_revoked"` (task 04).
- `cli.Run` resolves "step zero" (leftovers, an unfinished pairing, a
  rejected credential) before calling `Flow.StartNew`; after a rejection it
  prints `The server no longer accepts this computer's credential. Starting a new pairing.`,
  deletes the old pairing, and starts a new one (`runRejectedRePair`).
- `pair.StartNew` sends `hostname`, `os_name`, `os_version`
  (`computer.go`).

### The identity: `internal/worker/pair/identity.go` (new)

```go
// appID is tervi's application ID for deriving the computer's identity
// from /etc/machine-id. Never change it: every paired computer would look
// new to the server.
var appID = [16]byte{0xca, 0x6e, 0x70, 0xbc, 0x29, 0xab, 0x4e, 0xd1,
    0xbc, 0x52, 0x60, 0x07, 0x94, 0xcb, 0xb1, 0x22}
```

(`ca6e70bc29ab4ed1bc52600794cbb122`, made once with `systemd-id128 new`.)

- Read `/etc/machine-id`. The path is a new field,
  `pair.Options.MachineIDPath` (empty means `/etc/machine-id`). Every test
  that builds `pair.Options` (in `internal/worker/pair` and `test/e2e`) sets
  it, to a file of its own or to a missing path together with `--yes`, so
  no test reads the real `/etc/machine-id` and no result depends on the
  test computer. Task 08 uses this field too.
- The file must be exactly 32 hex characters, optionally followed by one
  newline; uppercase is accepted. Parse them into 16 bytes.
- The identity is HMAC-SHA256 with those 16 bytes as the key and the 16
  bytes of `appID` as the message: all 32 bytes of the result, written as 64
  lowercase hex characters. This is systemd's method for an
  application-specific ID, without its shortening to 128 bits.
- A missing or unreadable file, an empty one, `uninitialized`, or any other
  content means "no machine ID".
- The machine ID and the identity are never printed, logged, or written to
  any file.

### Sending it: `internal/worker/pair`

`StartNew` adds `identity_hash` to the start request when there is an
identity, and leaves the field out when there is none.

### The question: `internal/worker/cli/machineid.go` (new)

`tervi pair` asks before step zero changes anything, so a "no" really
changes nothing. The `Flow` interface gets the method
`HasMachineID() bool`, implemented in `pair` (which `cli` cannot import).
The question and reading its answer live in the new file
`internal/worker/cli/machineid.go`; `cli.go` only parses `--yes` and calls
it, so `cli.go` stays under 300 lines. With no machine ID and no `--yes`:

```text
! tervi can't read this computer's machine ID (/etc/machine-id).
  Without it, the server won't recognize this computer if you pair it
  again later: it will come back as a new computer, without its history.
  To fix this, create a machine ID first: sudo systemd-machine-id-setup
Pair without it? [y/N]
```

Spaces around the answer are removed first, then `y` or `yes` in any case
continues without an identity (as in R10); anything else, including an empty line or the end of input,
prints `Nothing was changed.` and exits `1`. `--yes` skips the question.
`tervi pair --server <url> --yes` and `--yes --server <url>` both work;
update the usage line.

When the computer has a machine ID, nothing new is printed.

`machineid.go` holds one helper that asks when needed and reports whether
to go on. It is called at the start of `runLeftover` (`leftover.go`), at the
start of the rejected and revoked re-pair functions (`handover.go`), before
`checkAndStart` in `cli.go`'s branch with no pairing at all, and in the
`cli.StartNewPairing` handling (below), so `cli.go` gains only a few lines.

The question is asked only on the paths that go on to start a new pairing
(`Flow.StartNew`): no pairing yet, leftover pairing data, a rejected
credential, and a revoked one; and on those paths before anything is
deleted. It is never asked when the computer is already paired, when an
unfinished pairing is resumed (`FinishEarlier`, which sends no identity),
or when `tervi pair` refuses because another server's pairing is
unfinished. The one exception is a resumed pairing that turns into a new
one (next part): it is asked there, before the old pairing is deleted.

### After a revocation (R22)

In step zero, a confirmed pairing with `stop_reason: "credential_revoked"`
is handled like the rejected case, with the line
`This computer was unpaired on the server. Starting a new pairing.`

The acknowledgment route now also answers `401 credential_revoked`
(task 01). Today `pair/confirm.go` treats any `401` other than
`unknown_credential` as "✗ Unexpected answer from the server." Case: a
pairing whose acknowledgment answer was lost, whose machine was then
revoked on the page; every `tervi pair` would repeat that message. This
can only happen when `tervi pair` resumes that pairing
(`Flow.FinishEarlier`).

- In `pair`, an acknowledgment answered `401 credential_revoked` makes
  `FinishEarlier` return the new exported constant `cli.StartNewPairing`
  (a negative number, never used as an exit code), without printing or
  deleting anything.
- In `cli`, that value is handled like the revoked case of step zero:
  ask the machine ID question if needed (a "no" changes nothing), print
  R22's line, delete the old pairing (`deletePairing`), and call
  `checkAndStart`, so the handover after the new pairing uses the new
  credential.
- The save-failure report's answer stays ignored, as in slice 1
  (`reportSaveFailure` discards it): the credential it reports is always
  the one just issued, still `pending`, which nothing revokes.

**One answer reader.** `tervi pair`'s machine ID question and
`tervi unpair`'s questions read the answer with one function in
`internal/worker/cli/answer.go`. It reads one byte at a time up to a
newline or the end of input, so nothing typed after the answer (such as
the pairing code) is taken; end of input after some bytes counts as the
answer. Then it removes spaces around the answer and accepts `y` or `yes`
in any case (R10). Each question's last line ends with `[y/N] ` (one space)
and no newline, so the answer is typed on the same line, as the spec's
"What the user does" shows (fix 1).

## Boundaries

- May create or change: `internal/worker/pair/`, `internal/worker/cli/`
  (the new `machineid.go`, `cli.go`, `handover.go`, `leftover.go`, the
  `Flow` interface, the usage line; a new `answer.go` and `unpair.go`, only
  so both `[y/N]` questions share one answer reader, above),
  `cmd/tervi/` (wiring only), their tests, `test/e2e/` helpers and tests
  that build `pair.Options` (only to set `MachineIDPath`).
- Must not change: `internal/worker/daemon/`, `internal/worker/control/`,
  `internal/worker/state/`, `internal/worker/service/`, anything under
  `internal/server/`, `api/`, `design/`, `slices/` except this task's row
  in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestIdentityKnownValue` — a fixed machine ID gives a fixed 64-character identity (written in the test), and differs from the plain SHA-256 of the machine ID | R23 |
| `TestIdentitySameEachTime` — the same file gives the same identity on every call; another machine ID gives another | R23, R29 |
| `TestIdentityParsing` — uppercase and a trailing newline are accepted; missing, empty, `uninitialized`, 31 or 33 characters, and non-hex mean "no machine ID" | R23 |
| `TestStartSendsIdentity` — the start request carries `identity_hash`; with no machine ID it has no such field | R23 |
| `TestIdentityNeverShown` — neither the machine ID nor the identity appears in any output line or file the worker writes | R23 |
| `TestNoMachineIDQuestion` — the five lines; `y` continues without an identity; `n`, empty, end of input: `Nothing was changed.`, exit `1`, and step zero has removed nothing | R23 |
| `TestNoMachineIDYes` — `--yes` (both flag orders) asks nothing and continues | R23 |
| `TestPairAfterRevoked` — a revoked pairing prints R22's line, removes the old pairing, and starts a new one | R22 |
| `TestConfirmRevoked` — resuming a pairing whose acknowledgment answers `401 credential_revoked`: `FinishEarlier` returns `cli.StartNewPairing`; then R22's line, the old pairing removed, a new pairing started, and the handover uses the new credential; with no machine ID and `n`, nothing is removed | R22, R23 |
| `TestQuestionOnlyWhenStarting` — with no machine ID: already paired, resuming an unfinished pairing, and another server's unfinished pairing ask nothing; no pairing yet, leftovers, rejected, and revoked ask | R23 |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- Matching on the server and the approval page (task 03).
- The end-to-end test of pairing the same computer again (task 08).

## If anything is unclear

Stop and report the question. Do not guess.
