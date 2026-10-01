# Worker Architecture

Module map for the worker component. One row per module, each marked **built**,
**planned**, or **undecided**.

Current round: [0001 worker pairing](../../specs/0001-worker-pairing/scope.md)
([build](../../specs/0001-worker-pairing/build.md)). See also the
[system overview](../architecture.md).

"Built" means a module exists in source. Round 0001 built `connections`; it is
the only built row so far.

## Modules

| Module | Source path | Status | Purpose |
| --- | --- | --- | --- |
| `connections` | `worker/internal/connections/` | **built** - 0001 | Pair this worker with a server and store its credential |

The command is `tervi`, at `worker/cmd/tervi/`. Add a module when a round needs
it; do not create one ahead of time.

## `connections`

| Field | Value |
| --- | --- |
| Purpose | Pair this worker installation with a server, persist the result, and hold the connection to that server. |
| Owns | One pairing attempt end to end; detecting OS and hostname; displaying the approval link; polling with the polling secret; storing the credential through a secret-store interface; the local registration file; status-first recovery; **since 0002**, one connection attempt end to end and holding the connection open. |
| Does not own | Assigning machine IDs, server-side approval or credential verification, server records, agent spawning, project files, IDE services, or job execution. |
| Public operations | Run one pairing attempt; run one connection attempt; load saved registration; acquire and release the local guard; store and load a credential. There is no removal operation. [build.md section 3](../../specs/0001-worker-pairing/build.md#3-worker-capability-boundaries) defines these as semantic operations, deliberately not final Go signatures. |
| Inputs | The intended HTTPS server address, detected OS and hostname, secret-store access, and cancellation. |
| Outputs | Progress and a typed final result; a stored credential; a durable local registration. Never prints the polling secret or the credential. |
| Local state | Server address, registration identifiers, credential reference, `connection_intent`, and `pairing_phase` in one JSON file in a private per-user directory. The usable credential lives only in the OS secret store. **No worker database.** |
| Allowed dependencies | Narrow interfaces for its remote API, registration store and guard, and secret store. Shared wire types under `internal/protocol/`. Never server-private packages, never one general-purpose platform interface. |
| Credential boundary | Scope the credential to its registration. Authenticate only to the intended server; an explicitly supplied server must match the saved one. Never forward a proof through a redirect or to a different server. |
| Failure behavior | Report an unavailable or locked secret store and stop - **never write a plaintext fallback**. Distinguish an unknown outcome from a confirmed rejection. Never infer revocation from a transport failure. Never report completion before both local writes succeed. |
| First round | [0001](../../specs/0001-worker-pairing/scope.md) |

The command owns terminal rendering and the attempt lifecycle. `-d` selects the
reserved detached mode, which in 0001 does not fork, install a service, or keep
a process alive.

## Pairing, as the worker sees it

The worker shows an **approval link** and nothing else:

    https://platform.example/pair/<approval-key>

There is no code for the user to type. The secret is in the link, and the user
may open it on another computer or phone. The worker never holds the approval
key - the server returns a separate polling secret, which the worker keeps in
memory for that attempt only and never prints.

Operations and retry semantics are in
[contracts/server-machines](../../contracts/server-machines/README.md).

## Persistence and authentication

A machine ID identifies a registration. It is not a secret and proves nothing.
The worker retrieves the usable credential from the OS secret store and presents
it over TLS; the server verifies it against its retained hash.

Supported this round: **Windows 11 Credential Manager** and **Linux desktop
Secret Service**, during an interactive session with the store unlocked. A
locked or missing store is an explicit setup error with no automatic fallback.
macOS, headless Linux, and unattended unlocking are out of scope - see
[scope.md section 1.3](../../specs/0001-worker-pairing/scope.md#13-persistent-intention-and-scope).

The state file lives outside project directories with private permissions and
never contains credential plaintext. Copying it to another computer transfers
nothing, because the credential is not in it. Each computer pairs
independently. A restart with valid saved state keeps the same machine ID.

Corrupted or inconsistent local state stops and reports. Nothing is repaired,
replaced, or deleted automatically, and a network error never triggers local
overwrite or a new pairing.

## Contract ownership

The worker consumes
[contracts/server-machines](../../contracts/server-machines/README.md). It owns
no HTTP contract of its own and keeps no second copy of a schema.

## Deferred

Heartbeat, automatic reconnect, user-initiated disconnect, OS-startup recovery,
real process detachment, startup services, workspace registration, and agent
execution.

**Opening a connection is no longer deferred.** Round 0002 opens one and holds
it in the foreground. What remains deferred is everything that keeps it: noticing
when it dies, reconnecting, and turning it off again.

See [0001 scope.md section 1.3](../../specs/0001-worker-pairing/scope.md#13-persistent-intention-and-scope)
and [0002 scope.md section 1.3](../../specs/0002-connection-establishment/scope.md#13-scope)
for what is later, what is undecided, and what is permanently ruled out.
