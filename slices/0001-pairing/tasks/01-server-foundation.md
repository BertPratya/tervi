# Task 01 — Server foundation

Slice: 0001-pairing    Risk: core    Depends on: —

## Goal

Give the server what every later pairing task needs: a PostgreSQL connection,
migrations that create the two tables, settings that keep the server on this
computer only, and a package for secret values that can never be printed by
accident.

## Requirements (copied from the spec)

- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired. *This task only
  creates the `expires_at` columns; later tasks use them.*
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs. *This task provides the
  secret type that later tasks use so that no secret is ever printed.*
- R21. **Known limitation of this slice:** without sign-in, anyone who can
  reach the server can pair a computer with it, because whoever runs
  `tervi pair` sees the approval link and can approve it. This slice is
  therefore limited to a server reachable only from its own computer
  (`localhost`). Sign-in, so that only the server's owner can approve, shall
  exist before the server is reachable from any other computer.
- R22. The server shall store only hashes of the approval key, the polling key,
  and the credential. The pairing code may be stored as it is, because it is
  useless without the polling key and lives at most 10 minutes.

## Context

### Existing code

`cmd/server/main.go` already serves `GET /health` (answers `200` with body
`ok`) and listens on `SERVER_ADDR`, defaulting to `:8080`. Keep `/health`
working. `go.mod` already requires `github.com/jackc/pgx/v5` and
`github.com/joho/godotenv`.

### Settings (read by `cmd/server`)

| Variable | Default | Rule |
| --- | --- | --- |
| `SERVER_ADDR` | `127.0.0.1:8080` | Where the server listens. Change the current default `:8080`, which listens on every network interface. |
| `TERVI_PUBLIC_URL` | `http://localhost:8080` | The address put into approval links by later tasks. Must start with `http://` or `https://` and have a host; otherwise the server refuses to start with a clear message. |
| `DATABASE_URL` | none | Required. The server refuses to start without it. |

If a `.env` file exists in the working directory, load it with `godotenv`
before reading the variables; real environment variables win over `.env`.
Update `.env.example` to show the new `SERVER_ADDR` default and
`TERVI_PUBLIC_URL`.

### Package `internal/db`

- `Open(ctx, databaseURL)` returns a `*pgxpool.Pool`.
- `Migrate(ctx, pool)` runs the embedded migrations with
  `github.com/pressly/goose/v3` (new dependency). Migrations are `.sql` files
  in `internal/db/migrations/`, embedded with `go:embed`.
- `cmd/server` runs `Migrate` at start, before serving.

### Migration `0001_pairing.sql`

Table `pairing_requests`:

| Column | Type |
| --- | --- |
| `id` | `uuid` primary key, default `gen_random_uuid()` |
| `hostname`, `os_name`, `os_version` | `text not null default ''` (empty means unknown) |
| `status` | `text not null`, check: one of `waiting_for_approval`, `waiting_for_code`, `finishing`, `paired`, `rejected`, `failed`, `expired` |
| `polling_key_hash` | `bytea not null unique` |
| `approval_key_hash` | `bytea not null unique` |
| `pairing_code` | `text` (null until accepted) |
| `tries_left` | `integer not null default 5` |
| `failure_reason` | `text`, null, or one of `wrong_codes`, `not_saved`, `not_confirmed` |
| `created_at` | `timestamptz not null default now()` |
| `expires_at` | `timestamptz not null` |

Table `machines`:

| Column | Type |
| --- | --- |
| `machine_id` | `uuid` primary key, default `gen_random_uuid()` |
| `pairing_request_id` | `uuid not null unique`, references `pairing_requests(id)` |
| `hostname`, `os_name`, `os_version` | `text not null default ''` |
| `display_name` | `text not null` |
| `credential_hash` | `bytea not null unique` |
| `status` | `text not null`, check: one of `pending`, `active`, `failed`, `expired` |
| `created_at` | `timestamptz not null default now()` |
| `expires_at` | `timestamptz not null` |

### Package `internal/secret`

One type, `secret.Value`, holds any secret: an approval key, a polling key, a
credential, or a pairing code.

- **Its printed form is always `[hidden]`**: with `fmt` (every verb, including
  `%v`, `%+v`, `%#v`, `%s`, `%q`, `%x`), with `log/slog` (implement
  `slog.LogValuer`), and with `encoding/json` (marshals to `"[hidden]"`).
- `Reveal()` returns the real string. It is the only way to read the value.
- `NewKey()` makes a key or credential: 32 bytes from `crypto/rand`, encoded as
  base64url without padding (43 characters).
- `NewPairingCode()` makes a code: 8 digits from `crypto/rand`, each digit
  equally likely, formatted `NNNN-NNNN` (for example `4827-1934`).
- `FromString(s)` wraps a value received from outside (a header or a request).
- `Hash(v)` returns the SHA-256 of `v.Reveal()` as `[]byte`.
- `NormalizeCode(s)` removes `-` and spaces, so `4827 1934` and `48271934`
  both become `48271934`.

### Tests and the database

Tests that need PostgreSQL read `DATABASE_URL` (start it with
`docker compose up -d`; the URL is in `.env.example`). If the database cannot
be reached from your environment, stop and report it; do not skip those tests.

## Boundaries

- May create or change: `cmd/server/`, `internal/db/`, `internal/secret/`,
  `.env.example`, `go.mod`, `go.sum`.
- Must not change: `slices/` except this task's row in `slices/0001-pairing/plan.md`,
  `AGENTS.md`, anything under `.claude/` or `scripts/`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestMigrateCreatesTables` — after `Migrate`, both tables exist with every column above | R15, R22 |
| `TestMigrateIsRepeatable` — running `Migrate` twice succeeds and changes nothing the second time | — |
| `TestHashColumnsAreUnique` — inserting two rows with the same `polling_key_hash` fails | R22 |
| `TestStatusCheck` — inserting a status outside the allowed list fails | — |
| `TestSecretNeverPrinted` — every `fmt` verb, `slog`, and `json.Marshal` of a `secret.Value` produce `[hidden]` and never the value | R18 |
| `TestNewKey` — 43 characters, base64url alphabet only, 1,000 keys are all different | R22 |
| `TestNewPairingCode` — matches `^\d{4}-\d{4}$`; 1,000 codes are not all equal | — |
| `TestNormalizeCode` — `4827-1934`, `4827 1934`, and `48271934` all give `48271934` | — |
| `TestHash` — equals the SHA-256 of the revealed value | R22 |
| `TestDefaultListenAddress` — with no `SERVER_ADDR`, the server listens on `127.0.0.1:8080` | R21 |
| `TestPublicURLMustBeValid` — `banana` and `ftp://x` are refused at start | — |
| `TestHealth` (existing) still passes | — |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Any HTTP endpoint other than `/health` (tasks 02, 03, 05), the approval page
(04), the expiry clean-up job (05), anything in the worker (06, 07).

## If anything is unclear

Stop and report the question. Do not guess.
