# Central Server Architecture

Module map for the server component. One row per module, each marked **built**,
**planned**, or **undecided**.

Current round: [0001 worker pairing](../../specs/0001-worker-pairing/scope.md)
([build](../../specs/0001-worker-pairing/build.md)). See also the
[system overview](../architecture.md).

"Built" means a module exists in source. Round 0001 built `machines`; it is the
only built row so far.

## Modules

| Module | Source path | Status | Purpose |
| --- | --- | --- | --- |
| `machines` | `server/internal/machines/` | **built** - 0001 | Worker registrations and the server side of pairing |
| `ownerauth` | `server/internal/ownerauth/` | planned - later round | Login, sessions, and owner identity. **Not in 0001.** |

Add a module when a round needs it. Do not create one ahead of time.

## `machines`

| Field | Value |
| --- | --- |
| Purpose | Own worker registrations, the server side of pairing, and the connections paired machines hold. |
| Owns | Pairing requests; generation and validation of the browser approval key and the worker polling secret; link-authorized approval without login; permanent machine IDs; reported machine descriptions; issuing, verifying, activating, and invalidating worker credentials; the temporary encrypted delivery copy and its lifecycle; **since 0002**, accepting machine connections and knowing which machines are connected. |
| Does not own | Login and sessions, the worker's local storage, workspace or agent execution, IDE services, or any work carried over the connections it holds. |
| Public operations | Start pairing; inspect a request; approve; deny; poll and deliver; registration status; acknowledge; expire; accept a connection. Wire shapes are in [contracts/server-machines](../../contracts/server-machines/README.md) - HTTP in [openapi.yaml](../../contracts/server-machines/openapi.yaml), the connection in [messages.md](../../contracts/server-machines/messages.md). |
| Inputs | Worker-reported OS and hostname; the approval key, polling secret, or worker credential presented as proof; server time. Never a client-supplied owner ID. |
| Outputs | Approval link and polling instructions; browser-safe request state; the issued credential, delivered only against the polling secret; registration status. |
| Persistent state | `pairing_requests`, `machines`, `worker_credentials`, `credential_deliveries` - defined in [server-machines.dbml](../data-model/server-machines.dbml). |
| Transient state | **Since 0002**, which machines are connected right now. It lives only in the running process, beside the connections themselves, and is deliberately not persisted: a stored value would survive the connections it describes and then assert something false. A restart discards both together. |
| Database | **PostgreSQL.** Uniqueness constraints and atomic conditional transitions carry the invariants; a process-local mutex does not. |
| Allowed dependencies | Its own transaction interface, shared wire types under `internal/protocol/`, randomness, clock, HTTP transport. Never the concrete storage package, never worker-private packages. |
| Authorization | Three separate proofs, none substituting for another. No owner identity exists this round. |
| Failure behavior | Enforce deadlines at the transition; reject invalid, expired, or revoked proofs; commit before reporting success; serialize competing decisions; keep a lost credential delivery recoverable until acknowledgment or expiry. |
| First round | [0001](../../specs/0001-worker-pairing/scope.md) |

Submodule boundaries for this round are in
[build.md sections 4-6](../../specs/0001-worker-pairing/build.md#4-server-capability-boundaries).

## Identity and data ownership

A machine is a registered worker installation, not a hardware fingerprint. The
server generates its permanent ID. Hostname and OS are descriptive, may be
duplicated, and never authenticate anything.

The server stores credential verifiers; the worker stores the usable secret in
its OS secret store. Machine, credential, and pairing records stay separate, so
machine identity survives a future credential change and a short-lived approval
key never becomes a permanent login secret.

**No owner identity exists in 0001.** Possession of the unpredictable approval
link authorizes the decision. No `owner_id` field is introduced, and no fake
owner is invented to fill one. Login and owner binding arrive in a later round
and will need their own design and a compatibility review.

## Contract ownership

[contracts/server-machines](../../contracts/server-machines/README.md) is
canonical for these HTTP operations. Consumers reference it; they never keep a
second copy of a schema.

`server/internal/web` serves the built browser assets. Page source lives under
`web/`. Page and API share one HTTPS origin. Neither asset serving nor browser
rendering decides whether a request may transition.

## Deferred

Heartbeat and liveness detection, automatic reconnect, disconnect and unpair
controls, credential rotation, multi-user sharing and administration, workspace
registration, and execution dispatch.

**Presence is no longer deferred.** Round 0002 accepts connections and tracks
which machines hold one. What remains deferred is knowing whether a tracked
connection is still *alive* - nothing yet detects a connection whose network
failed silently - and presence across more than one server instance.

See [0001 scope.md section 1.3](../../specs/0001-worker-pairing/scope.md#13-persistent-intention-and-scope)
and [0002 scope.md section 1.3](../../specs/0002-connection-establishment/scope.md#13-scope)
for what is later, what is undecided, and what is permanently ruled out.
