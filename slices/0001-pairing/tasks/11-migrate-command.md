# Task 11 — The migrate command

Slice: 0001-pairing    Risk: core    Depends on: 09, 10

## Goal

Stop the server from changing the database when it starts. Add a
`server migrate` command that applies migrations, and make the server check
the database version at start and refuse to run on the wrong one. Add a root
`README.md` that explains how to run tervi and how the code is laid out.

## Requirements (copied from the spec)

- R32. The server shall change the database structure only when its
  `migrate` command is run. If the database is not at the version the server
  expects, then the server shall refuse to start and say what to do.

## Context

### Today

`server.Run` (`internal/server/server.go`) calls `db.Migrate` at every start,
so starting the server creates and changes tables. `internal/server/db/db.go`
holds `Open` and `Migrate(ctx, pool) error`, which runs the embedded `.sql`
files with goose v3.27.0 (`github.com/pressly/goose/v3`).

### The commands

| Command | Does | Output | Exit code |
| --- | --- | --- | --- |
| `server migrate` | Applies every migration the database does not have yet | Standard output: `Database is up to date (version N).`, also when there was nothing to apply | `0` |
| `server migrate`, on any error (missing `DATABASE_URL`, database unreachable, a migration fails) | Nothing more | `logger.Error("migrate database", "error", err)` to standard error | `1` |
| `server` (no arguments) | Checks the database version, then serves as today | as today | as today |
| anything else | Nothing | Standard error: `Usage: server [migrate]` | `2` |

**Never print the password or the whole `DATABASE_URL`** in a message.
(Decided in code review round 1: a connection error naming the user, host,
or database is fine.) `db.Open` already reports a bad URL as
`invalid DATABASE_URL` without its value; keep it that way.

`server migrate` needs only `DATABASE_URL`. Add
`func LoadDatabaseURL() (string, error)` to `internal/server/config.go`. It
loads `.env` the same way `LoadConfig` does (share that code) and checks only
`DATABASE_URL`, with the same error as `LoadConfig` when it is missing. An
invalid `SERVER_ADDR` or `TERVI_PUBLIC_URL` must not stop `migrate`.

### The version check at server start

Before listening, `Run` compares the database's version with the newest
migration embedded in the program:

| Database | Error returned by `db.CheckVersion` |
| --- | --- |
| Same version | `nil` |
| No goose version table, or version `0` | `ErrNotMigrated`: `database is not migrated: run "server migrate" first` |
| Older (`0 < X < Y`) | `database is at version X, this program needs version Y: run "server migrate" first` |
| Newer (`X > Y`) | `database is at version X, newer than this program (version Y): use a newer program` |

`X` is the database's version and `Y` the newest embedded one. The older and
newer errors wrap `ErrVersionMismatch`, so `errors.Is` works on them.

`Run` returns the `CheckVersion` error **unwrapped**, before listening, and
`main` logs it with today's `logger.Error("server stopped", "error", err)`
and exits `1`.

**The check must not change the database, and must not wait.** goose's
`GetDBVersion`, `GetVersions`, and `HasPending` all create the
`goose_db_version` table (and insert a row) when it is missing, and
`GetDBVersion` takes the session lock when the provider has one. So
`CheckVersion`:

1. Asks PostgreSQL whether the table exists:
   `SELECT to_regclass('goose_db_version') IS NOT NULL`. If not, returns
   `ErrNotMigrated`.
2. Otherwise calls `GetVersions` on a provider created **without** a session
   lock, which reads the database's version and the newest embedded one.
3. Passes both to a pure function
   `versionError(dbVersion, newest int64) error`, which returns the error from
   the table above.

### Migrating safely

`Migrate(ctx, pool) (version int64, err error)` applies the migrations with
goose's PostgreSQL session lock, so two `migrate` runs at the same time are
safe: the second waits for the first, then finds nothing to apply.

```go
locker, err := lock.NewPostgresSessionLocker() // github.com/pressly/goose/v3/lock, part of the goose module
// ...
provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations,
    goose.WithVerbose(false), goose.WithSessionLocker(locker))
```

It returns the database's version after applying. Callers of today's
`Migrate` (`dbtest`, `db_test.go`) change to `_, err :=` or use the version.

### Code shape

- `internal/server/db/migrate.go`: `Migrate`, `CheckVersion`,
  `versionError`, `ErrNotMigrated`, `ErrVersionMismatch`. `db.go` keeps
  `Open` and the embedded files.
- `internal/server/server.go`: `Run` calls `db.CheckVersion` instead of
  `db.Migrate`.
- `cmd/server/main.go`: add
  `func run(ctx context.Context, args []string, stdout, stderr io.Writer) int`,
  holding today's logic plus the `migrate` branch, with the logger writing to
  `stderr`. `main` only sets up the signal context, calls `run` with
  `os.Args[1:]`, and exits with its result.

### Test databases

`dbtest.New(t)` keeps giving tests a migrated database. Add
`dbtest.NewEmpty(t) (pool *pgxpool.Pool, databaseURL string)`, which creates
a database with no tables at all and drops it at test cleanup, like `New`.

`databaseURL` must point at that new database: take `DATABASE_URL`, parse it
with `net/url`, and replace only the path with `/<new database name>`. Do
not use `pool.Config().ConnString()`: it returns the original `DATABASE_URL`,
which is the developer's real database. If `DATABASE_URL` is not in URL form
(`postgres://…`), fail the test with a message saying so.

Tests in `cmd/server` pass the URL with `t.Setenv("DATABASE_URL", url)`.

### README.md (new, at the repository root)

Keep it short:

1. One paragraph: what tervi is (see the first line of `AGENTS.md`).
2. How to run it:
   ```bash
   docker compose up -d
   go run ./cmd/server migrate
   go run ./cmd/server
   go run ./cmd/tervi pair --server http://localhost:8080
   ```
3. The layout: the "Code layout" table from `AGENTS.md`, then the folder
   tree of `cmd/` and `internal/` (folders only, one line each).

### AGENTS.md "Commands"

Replace the command block with:

```bash
docker compose up -d           # local PostgreSQL
go run ./cmd/server migrate    # apply database migrations (the server never does)
go run ./cmd/server            # start the server
go vet ./...
go test ./...
```

## Boundaries

- May create or change: `cmd/server/`, `internal/server/` (only `config.go`,
  `server.go`, their tests, and `db/`), `README.md`, the "Commands" block of
  `AGENTS.md`, and this task's row in `slices/0001-pairing/plan.md`.
- Must not change: `internal/server/pairing/`, `internal/server/web/`,
  `internal/worker/`, `internal/secret/`, `cmd/tervi/`, the migration's
  content, the rest of `AGENTS.md`, `slices/` (except that row), `docs/`,
  `.claude/`, `.codex/`, `scripts/`.
- Do not add dependencies; `goose/v3/lock` is part of the goose module
  already in `go.mod`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestMigrateCommand` (`cmd/server`) — on a `dbtest.NewEmpty` database: `run(ctx, ["migrate"], …)` returns `0` and prints `Database is up to date (version 1).`; the pairing tables exist; a second call returns `0` with the same line and adds no row to `goose_db_version` | R32 |
| `TestMigrateNeedsOnlyDatabaseURL` (`cmd/server`) — with `SERVER_ADDR=0.0.0.0:8080` set, `migrate` still returns `0` | R32 |
| `TestMigrateErrorHidesURL` (`cmd/server`) — with `DATABASE_URL` pointing at a closed port and containing the password `hunter2-test`, `migrate` returns `1`, and neither output contains `hunter2-test` | R32 |
| `TestUnknownArgument` (`cmd/server`) — `run(ctx, ["serve"], …)` returns `2` and writes `Usage: server [migrate]` to standard error | R32 |
| `TestMigrateConcurrent` (`internal/server/db`) — two `Migrate` calls started at the same time on an empty database both succeed, and `goose_db_version` holds each applied version once | R32 |
| `TestCheckVersion` (`internal/server/db`) — migrated → `nil`; empty database → `ErrNotMigrated`, and afterwards the database still has no tables (including no `goose_db_version`); a version row above the newest embedded migration → the "newer" message, and `errors.Is(err, ErrVersionMismatch)` | R32 |
| `TestVersionError` (`internal/server/db`) — a table test of `versionError`: (0, 1) → not migrated; (1, 2) → the "older" message with both numbers; (1, 1) → `nil`; (3, 1) → the "newer" message with both numbers | R32 |
| `TestRunRefusesUnmigratedDatabase` (`internal/server`) — `Run` with a `dbtest.NewEmpty` database returns an error with `errors.Is(err, db.ErrNotMigrated)`, nothing accepts connections on the configured address, and the database still has no tables | R32 |

Tests in `internal/server/db` that use `dbtest` must be in package `db_test`,
because `dbtest` imports `db`. `TestVersionError` tests the unexported
`versionError`, so it goes in a separate file in package `db`; Go allows both
in one folder.

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- Moving files or packages (tasks 09 and 10).
- Down migrations or a `migrate down` command.
- The OpenAPI description and the Swagger page (a later slice).

## If anything is unclear

Stop and report the question. Do not guess.
