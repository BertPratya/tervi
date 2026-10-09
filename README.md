# tervi

tervi lets a user run coding agents on their own computers from a browser. The Go server coordinates pairing, and the Go worker runs on the user's computer.

## Run tervi

```bash
docker compose up -d
go run ./cmd/server migrate
go run ./cmd/server
go run ./cmd/tervi pair --server http://localhost:8080
```

## Code layout

| Where | Holds |
| --- | --- |
| `cmd/<program>/` | One folder per program: only `main`, which connects the packages |
| `internal/server/` | The server's assembly: settings, routes, start and stop |
| `internal/server/<feature>/` | Server code |
| `internal/worker/<feature>/` | Worker code |
| `internal/<feature>/` | Code used by both the server and the worker |
| `api/` | The contracts, and the Go package `api` that embeds them |
| `test/<suite>/` | Tests that run the real programs together (`e2e`, `contract`) |

```text
cmd/
├── server/
└── tervi/
internal/
├── secret/
├── server/
│   ├── db/
│   │   ├── dbtest/
│   │   └── migrations/
│   ├── pairing/
│   └── web/
│       └── static/
└── worker/
    ├── cli/
    ├── credential/
    ├── pair/
    └── state/
```
