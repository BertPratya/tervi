# Task 01 — Server foundation

Slice: 0001-pairing    Risk: core    Depends on: —

## Goal

Give the server what every later pairing task needs: settings that keep it on
this computer only, a PostgreSQL connection with migrations for both tables,
an importable server assembly, and a secret type that can never be printed by
accident.

## Requirements (copied from the spec)

- R15. If the pairing is not approved and the correct code typed within 10
  minutes of starting, then the pairing shall expire: the worker shall say so
  and stop, and the link shall show that it has expired. *This task only
  creates the `expires_at` columns; later tasks use them.*
- R18. The worker shall save the credential in the OS secret store, and never
  in a plain file, in terminal output, or in logs. *This task provides the
  secret type every later task uses so that no secret is ever printed.*
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

`cmd/server/main.go` serves `GET /health` (answers `200` with body `ok`) and
listens on `SERVER_ADDR`, defaulting to `:8080`. Its test `TestHealth` is in
`cmd/server/main_test.go`. `go.mod` already requires `github.com/jackc/pgx/v5`
and `github.com/joho/godotenv`.

### Settings

| Variable | Default | Rule |
| --- | --- | --- |
| `SERVER_ADDR` | `127.0.0.1:8080` | Where the server listens. The host must be a loopback address: `127.0.0.1`, `::1`, or `localhost`. Any other host, including an empty one (`:8080`) or `0.0.0.0`, makes the server refuse to start with: `SERVER_ADDR must be a loopback address (127.0.0.1, ::1, or localhost) until sign-in exists.` |
| `TERVI_PUBLIC_URL` | `http://localhost:8080` | The address put into approval links by later tasks. Must start with `http://` or `https://` and have a host; otherwise the server refuses to start with a clear message. A trailing `/` is removed. |
| `DATABASE_URL` | none | Required. The server refuses to start without it. |

If a `.env` file exists in the working directory, load it with `godotenv`
before reading the variables; real environment variables win over `.env`.
Update `.env.example` to show the new `SERVER_ADDR` default and
`TERVI_PUBLIC_URL`.

### Package `internal/server`

Importable, so the end-to-end test can run the real server.

- `LoadConfig() (Config, error)` reads and checks the settings above.
- `New(cfg Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler`
  builds one `http.ServeMux` with `GET /health`, then calls
  `pairing.Register(mux, pairing.Deps{Pool: pool, PublicURL: cfg.PublicURL, Logger: logger})`
  and `web.Register(mux)`.
- `Run(ctx, cfg, logger) error` opens the database, runs `Migrate`, starts
  `pairing.StartCleanup(ctx, pool, logger)`, and serves `New(...)` on
  `cfg.ServerAddr` until `ctx` is cancelled.
- `cmd/server/main.go` becomes a thin wrapper: load settings, make a logger
  writing to standard error, call `Run`.

Create the two packages the server calls, as **empty stubs** that later tasks
fill in, so each later task changes only its own package:

- `internal/pairing`: `type Deps struct{ Pool *pgxpool.Pool; PublicURL string; Logger *slog.Logger }`,
  `func Register(mux *http.ServeMux, d Deps)` (registers nothing yet),
  `func StartCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger)` (does nothing yet).
- `internal/web`: `func Register(mux *http.ServeMux)` (registers nothing yet).

**Never log request paths.** No request logging middleware: a path under
`/pair/` contains an approval key.

### Package `internal/db`

- `Open(ctx, databaseURL)` returns a `*pgxpool.Pool`.
- `Migrate(ctx, pool)` runs the embedded migrations with
  `github.com/pressly/goose/v3` (new dependency). Migrations are `.sql` files
  in `internal/db/migrations/`, embedded with `go:embed`.

### Migration `0001_pairing.sql`

Table `pairing_requests`:

| Column | Type |
| --- | --- |
| `id` | `uuid` primary key, default `gen_random_uuid()` |
| `hostname`, `os_name`, `os_version` | `text not null default ''` (empty means unknown) |
| `status` | `text not null`, check: one of `waiting_for_approval`, `waiting_for_code`, `finishing`, `paired`, `rejected`, `failed`, `expired` |
| `polling_key_hash` | `bytea not null unique` |
| `approval_key_hash` | `bytea not null unique` |
| `pairing_code` | `text` (null until the Accept button is clicked) |
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

- **The value is stored behind a pointer** (for example `struct{ p *string }`).
  Go's printing reaches into struct fields without asking the type, so a plain
  string field would leak when a `Value` sits inside another struct; behind a
  pointer, only an address is printed.
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

### Tests and the database — `internal/db/dbtest`

`go test ./...` runs packages at the same time, so tests must never share
tables. Add a helper every database test in the project uses:

- `dbtest.New(t *testing.T) *pgxpool.Pool` connects to the server in
  `DATABASE_URL`, creates a new database with a random name
  (`tervi_test_<random>`), runs `Migrate` on it, and returns a pool to it.
  `t.Cleanup` closes the pool and drops the database.
- If `DATABASE_URL` is not set or the server cannot be reached, the helper
  fails the test with a clear message. It never skips.

Start PostgreSQL with `docker compose up -d`; the URL is in `.env.example`.
If the database cannot be reached from your environment, stop and report it.

## Boundaries

- May create or change: `cmd/server/`, `internal/server/`, `internal/db/`,
  `internal/secret/`, `internal/pairing/` (only the stubs above),
  `internal/web/` (only the stub above), `.env.example`, `go.mod`, `go.sum`.
- Must not change: `slices/` except this task's row in `slices/0001-pairing/plan.md`,
  `AGENTS.md`, anything under `.claude/` or `scripts/`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestDBTestIsolated` — two `dbtest.New` pools in one test point at different databases; a row inserted in one is not visible in the other; after cleanup, both databases are gone | Tests never share tables |
| `TestMigrateCreatesTables` — after `Migrate`, both tables exist with every column above | R15, R22 |
| `TestMigrateIsRepeatable` — running `Migrate` twice succeeds and changes nothing the second time | Migrations run at every start |
| `TestHashColumnsAreUnique` — two rows with the same `polling_key_hash` cannot both be inserted | R22 |
| `TestStatusCheck` — a status outside the allowed list cannot be inserted | The database rejects impossible states |
| `TestSecretNeverPrinted` — every `fmt` verb, `slog`, and `json.Marshal` of a `secret.Value` give `[hidden]` and never the value | R18 |
| `TestSecretNestedNeverPrinted` — a `Value` in an unexported field of another struct, printed with `%v`, `%+v`, and `%#v`, never shows the value | R18 |
| `TestNewKey` — 43 characters, base64url alphabet only; 1,000 keys are all different | R22 |
| `TestNewPairingCode` — matches `^\d{4}-\d{4}$`; 1,000 codes are not all equal | Code format |
| `TestNormalizeCode` — `4827-1934`, `4827 1934`, and `48271934` all give `48271934` | Code format |
| `TestHash` — equals the SHA-256 of the revealed value | R22 |
| `TestDefaultListenAddress` — with no `SERVER_ADDR`, the address is `127.0.0.1:8080` | R21 |
| `TestListenAddressMustBeLoopback` — `:8080`, `0.0.0.0:8080`, and `192.168.1.5:8080` are refused with the message above; `127.0.0.1:9000`, `[::1]:8080`, and `localhost:8080` are accepted | R21 |
| `TestPublicURL` — `banana` and `ftp://x` are refused; `http://localhost:8080/` becomes `http://localhost:8080` | Links never contain `//pair/` |
| `TestHealth` — `GET /health` through `server.New` still answers `200 ok` | Existing behavior kept |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

Any route other than `/health` (tasks 02, 03, 04), the clean-up job's work
(04), anything in the worker (05, 06, 07).

## If anything is unclear

Stop and report the question. Do not guess.
