# Task 01 — Server: credentials in their own table

Slice: 0003-disconnect-unpair    Risk: core    Depends on: —

## Goal

Move each machine's credential out of `machines` into a new table,
`credentials`, so a machine keeps one row while each pairing adds a
credential row. Every server query that used the moved columns uses the new
table, with no change in behavior except one: the connection route answers
a `revoked` credential with `401 credential_revoked`. Add the close code
`4002` and a way to close one machine's connection with it, for tasks 02
and 03.

## Requirements (copied from the spec)

- R18. When the user confirms, the server shall mark the machine revoked,
  refuse its credential from then on, close its connection if it is
  connected, and stop listing it. The page shall stop showing it at once.
  If the request fails, the row shall stay and the page shall show
  `Couldn't revoke <name>. Try again.`
  *This task: the `revoked` status, refusing a revoked credential on the
  connection route, and closing a machine's connection with `4002`. The
  route that does the revoking is task 02.*
- R19. Unpaired and revoked machines shall not appear on the Machines page.
  The server shall keep their records.
  *This task: the machines list shows only machines with an `active`
  credential, and no row is ever deleted.*
- R20. When the server refuses this computer's credential because it was
  revoked, the daemon shall stop trying, as in slice 2's R18, and write the
  log line
  `✗ This computer was unpaired on the server. Pair it again with: tervi pair --server <server>`.
  *This task: the server's side, `401 credential_revoked`. The daemon is
  task 04.*

## Context

### The migration: `internal/server/db/migrations/0003_credentials.sql`

A goose migration, like `0001` and `0002`. Up, in this order:

```sql
CREATE TABLE credentials (
    credential_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    machine_id uuid NOT NULL REFERENCES machines(machine_id),
    pairing_request_id uuid NOT NULL UNIQUE REFERENCES pairing_requests(id),
    credential_hash bytea NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('pending', 'active', 'failed', 'expired', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    CHECK ((status = 'revoked') = (ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX credentials_one_active_per_machine
    ON credentials (machine_id) WHERE status = 'active';
CREATE INDEX credentials_machine ON credentials (machine_id);

INSERT INTO credentials (machine_id, pairing_request_id, credential_hash, status, created_at, expires_at)
    SELECT machine_id, pairing_request_id, credential_hash, status, created_at, expires_at FROM machines;

ALTER TABLE machines
    DROP COLUMN pairing_request_id,
    DROP COLUMN credential_hash,
    DROP COLUMN status,
    DROP COLUMN expires_at,
    ADD COLUMN identity_hash bytea CHECK (identity_hash IS NULL OR length(identity_hash) = 32);
CREATE INDEX machines_identity_hash ON machines (identity_hash);

ALTER TABLE pairing_requests
    ADD COLUMN identity_hash bytea CHECK (identity_hash IS NULL OR length(identity_hash) = 32),
    ADD COLUMN matched_machine_id uuid REFERENCES machines(machine_id);
```

Down undoes it: drop the two `pairing_requests` columns and
`machines.identity_hash`; add back `machines.pairing_request_id`,
`credential_hash`, `status`, and `expires_at` and fill them from each
machine's most recently created credential (a `revoked` one becomes
`expired`); delete machines that have no credential; make the four columns
`NOT NULL` with their old rules (`UNIQUE`, the old `CHECK`); drop
`credentials`. Down is only for development; it may lose a machine's older
credentials.

The table is described in `design/data-model.md` (`credentials`,
`machines`, `pairing_requests`); the migration must agree with it.

### The credential check: `internal/server/credential/credential.go` (new package)

The package is named after what it provides. The connection, machines, and
pairing packages all use it; `connection` cannot import `machines`
(`machines` already imports `connection`), which is why it is its own
package.

```go
// Status names, exactly the database values.
const (
    StatusPending = "pending"
    StatusActive  = "active"
    StatusFailed  = "failed"
    StatusExpired = "expired"
    StatusRevoked = "revoked"
)

// Row is one credential as found by its hash.
type Row struct {
    CredentialID     string
    MachineID        string
    PairingRequestID string
    Status           string // pending past expires_at reads as expired
}

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface { QueryRow(ctx context.Context, sql string, args ...any) pgx.Row }

// Find hashes the proof with secret.Hash and reads its one row.
// found is false when no row has that hash.
func Find(ctx context.Context, q Querier, proof secret.Value) (row Row, found bool, err error)
```

`Find` reads by `credential_hash`, which is unique, so there is never more
than one row. A `pending` credential past `expires_at` is returned as
`expired`, as slice 1 reads it.

### Ending a credential: `internal/server/credential/revoke.go` (new)

```go
// Revoke sets the machine's active credential to revoked with
// ended_at = now(), inside the caller's transaction. It reports whether a
// credential changed; a machine with no active credential changes nothing.
func Revoke(ctx context.Context, tx pgx.Tx, machineID string) (changed bool, err error)
```

Tasks 02 and 03 call it. The caller closes the machine's connection after
its transaction commits (next part), so a worker that reconnects at once is
already refused.

### Closing one machine's connection: `internal/server/connection`

- `internal/protocol`: add `CloseRevoked = 4002`, next to `CloseTimeout`,
  and its test row.
- `List` gets an exported method:

  ```go
  // CloseRevoked closes the machine's connection, if it has one, with 4002
  // and removes it from the list at once, so the machine shows Offline.
  func (list *List) CloseRevoked(machineID string)
  ```

  It closes in the background through the same tracked close as
  `CloseAll`, so a server stopping at that moment still waits for it. The
  connection's own handler then finishes as for any close by the server and
  writes `last_seen_at`.

- **A connection being opened while its credential is revoked.** The
  connection route checks the credential, then accepts the WebSocket, then
  adds it to the list. A revoke that commits between the check and the add
  calls `CloseRevoked` while there is nothing to close, and the connection
  would stay open with a revoked credential. So, right after `add`
  succeeds, the route reads that credential's status again (by
  `credential_id`, from the row `credential.Find` returned); if it is no
  longer `active`, it closes the connection with `4002` and removes it, as
  `CloseRevoked` does. Tests reach the moment between the check and the add
  through a new field `AfterCheck func(machineID string)` in
  `connection.Deps`, called after the check and before the add, and nil in
  production.

### Queries that change

Every query that reads or writes `machines.credential_hash`, `status`,
`expires_at`, or `pairing_request_id` moves to `credentials`. Behavior stays
exactly as in slices 1 and 2:

| Where | Today | After |
| --- | --- | --- |
| `pairing/code.go` `issueCredential` | inserts a `machines` row with the credential | inserts a `machines` row (hostname, OS, display name, and `identity_hash` left null), then a `credentials` row (`pending`, `expires_at = now() + 5 minutes`) for it, in the same transaction; answers the machine's ID as today |
| `pairing/machine.go` `changeMachine` | updates `machines.status` | updates the `credentials` row found by hash; the result table is the same; a `revoked` credential answers `401 credential_revoked` |
| `pairing/status.go`, `approval.go` | `LEFT JOIN machines AS m ON m.pairing_request_id = pr.id` | `LEFT JOIN credentials AS c ON c.pairing_request_id = pr.id` for the status, and `LEFT JOIN machines AS m ON m.machine_id = c.machine_id` for `display_name` |
| `pairing/cleanup.go` | expires `pending` machines | expires `pending` credentials, and fails their pairing requests as today |
| `connection/route.go` | `WHERE credential_hash = $1 AND status = 'active'` | `credential.Find`: `active` opens; `revoked` answers `401 {"error": "credential_revoked"}`; anything else, or no row, `401 {"error": "unknown_credential"}` |
| `machines/machines.go` | `WHERE status = 'active'` | machines that have an `active` credential (a join or `EXISTS`), sorted as today |

Use `credential.Find` wherever a route finds a credential by its proof.
Write the shared SQL once (AGENTS.md).

### Tests that change

Tests that insert into or read `machines` directly (server packages,
`internal/server/db/dbtest`, `test/contract`, `test/e2e`) move to the new
columns. Keep each test's meaning; only its SQL changes. The e2e test that
makes the server reject a credential keeps doing so by the same means it
uses today, written for the new table.

### Contract

`api/openapi.yaml` already describes `credential_revoked`
(`RefusedCredential`) and `4002` is in `api/asyncapi.yaml`. The contract
test calls `openConnection`, `acknowledgeMachine`, and `reportSaveFailure`
with a revoked credential (set its row to `revoked` in the test) and checks
`401 credential_revoked`.

## Boundaries

- May create or change: `internal/server/db/migrations/0003_credentials.sql`,
  `internal/server/credential/`, `internal/server/connection/`,
  `internal/server/machines/machines.go`, `internal/server/pairing/`,
  `internal/protocol/`, every `_test.go` file and test helper that uses the
  moved columns (including `internal/server/db/`, `test/contract/`,
  `test/e2e/`), and every test that expects the newest migration to be
  version 2: `cmd/server/main_test.go` (`Database is up to date (version 2).`)
  and the version expectations in `internal/server/db/*_test.go` (for
  example, `migrate_test.go` uses version 3 as "newer than this program";
  make that 4).
- Must not change: `api/`, `design/`, anything under `internal/worker/`,
  the web pages, `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestMigrationMovesCredentials` — a database at version 2 with a `pending`, an `active`, a `failed`, and an `expired` machine migrates to version 3: each has one `credentials` row with the same hash, status, request, creation time, and expiry; `machines` has no credential columns; the new columns are null | R19 |
| `TestMigrationOneActivePerMachine` — a second `active` credential for one machine is refused by the database; a `revoked` row without `ended_at` is refused | R18 |
| `TestAcknowledgeRevoked` — the acknowledgment and save-failure routes answer `401 credential_revoked` for a `revoked` credential | R20, R22 |
| `TestFindCredential` — finds `active`, `revoked`, `failed`, and `expired` rows by proof; a `pending` row past its expiry reads as `expired`; an unknown proof is not found | R18, R20 |
| `TestRevokeCredential` — an `active` credential becomes `revoked` with `ended_at` set; a machine with none changes nothing and reports so | R18 |
| `TestRevokedWhileOpening` — a credential revoked between the route's check and adding the connection (through the test hook) is closed with `4002` right after it is added, and the machine is Offline | R18, R20 |
| `TestMigrationVersion` — `go run ./cmd/server migrate` reports version 3; the existing version tests expect 3, and 4 counts as newer than this program | R19 |
| `TestConnectionRefusesRevoked` — a revoked credential gets `401 credential_revoked` and no WebSocket; unknown, `pending`, `failed`, and `expired` get `401 unknown_credential` | R20 |
| `TestCloseRevoked` — a connected machine's connection closes with `4002` and the machine is Offline at once; a machine with no connection is a no-op; a server stopping meanwhile still waits for the close | R18 |
| `TestListOnlyMachinesWithActiveCredential` — a machine whose credential is `revoked`, `pending`, `failed`, or `expired` is not listed; its row is still in `machines` | R19 |
| Existing pairing, cleanup, connection, and machines tests pass unchanged in meaning | R18, R19 |
| Contract test — `openConnection`, `acknowledgeMachine`, and `reportSaveFailure` with a revoked credential answer `401 credential_revoked` | contract |

`go vet ./...` and `go test ./...` pass, and
`go run ./cmd/server migrate` brings a slice-2 database to version 3. This
task's row in `plan.md` changes to `done`.

## Out of scope

- The unpair and Revoke routes, and the Revoke button (task 02).
- Storing and matching the identity, and the approval page (task 03).
- Anything the worker does with `credential_revoked` or `4002` (task 04).

## If anything is unclear

Stop and report the question. Do not guess.
