# Task 07 — Worker: the computer's identity when pairing

Slice: 0003-disconnect-unpair    Risk: core    Depends on: 03, 04

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
  then `Pair without it? [y/N]` and wait. Only `y` or `yes` continues, and
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
  `systemd-machine-id-setup`).
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

- Read `/etc/machine-id` (the path is a field, so tests use a file of their
  own). It must be exactly 32 hex characters, optionally followed by one
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

### The question: `internal/worker/cli`

`tervi pair` asks before step zero changes anything, so a "no" really
changes nothing. The `Flow` interface gets a method that says whether the
identity can be computed (implemented in `pair`, which `cli` cannot
import). With no machine ID and no `--yes`:

```text
! tervi can't read this computer's machine ID (/etc/machine-id).
  Without it, the server won't recognize this computer if you pair it
  again later: it will come back as a new computer, without its history.
  To fix this, create a machine ID first: sudo systemd-machine-id-setup
Pair without it? [y/N]
```

`y` or `yes`, ignoring case and surrounding spaces, continues without an
identity; anything else, including an empty line or the end of input,
prints `Nothing was changed.` and exits `1`. `--yes` skips the question.
`tervi pair --server <url> --yes` and `--yes --server <url>` both work;
update the usage line.

When the computer has a machine ID, nothing new is printed.

### After a revocation (R22)

In step zero, a confirmed pairing with `stop_reason: "credential_revoked"`
is handled like the rejected case, with the line
`This computer was unpaired on the server. Starting a new pairing.`

## Boundaries

- May create or change: `internal/worker/pair/`, `internal/worker/cli/`
  (`cli.go`, `handover.go`, the `Flow` interface, the usage line),
  `cmd/tervi/` (wiring only), their tests, `test/e2e/` helpers that start
  pairings (only to pass or skip the question).
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

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md`
changes to `done`.

## Out of scope

- Matching on the server and the approval page (task 03).
- The end-to-end test of pairing the same computer again (task 08).

## If anything is unclear

Stop and report the question. Do not guess.
