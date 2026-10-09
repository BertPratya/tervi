# Task 12 — Contract test and API docs page

Slice: 0001-pairing    Risk: normal    Depends on: 09, 10, 11

## Goal

Make `api/openapi.yaml` a checked contract: a test runs the real server,
calls every operation, and fails when a request or an answer does not match
the file. Add a Swagger page at `/docs`, served only when the developer turns
it on, so people can read the API and try it in a browser.

## Requirements (copied from the spec)

None. This task checks the existing API against its contract and adds a
development tool.

## Context

### The contract

`api/openapi.yaml` (OpenAPI 3.0.3) describes every API route of slice 0001,
the approval page, and `/health`. Static files (`/static/…`) and the docs
page are not in it. Read it in full. Answers use `additionalProperties: false`, so an answer with an
unlisted field fails. If the test shows the server and the file disagree,
**stop and report** which operation, status, and field: do not change the
server or the file to make them agree.

### `api/api.go` (new)

```go
// Package api holds tervi's API contracts.
package api

import _ "embed"

// OpenAPI is the HTTP contract, api/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
```

Go's `embed` cannot reach files above a package's folder, so the contract is
embedded here and imported by the server and the tests.

### The contract test: `test/contract/contract_test.go` (new)

Use `github.com/getkin/kin-openapi` **v0.149.0** (new dependency):

- Load `api.OpenAPI` with `openapi3.NewLoader().LoadFromData`, and call
  `doc.Validate(ctx)`.
- Set `doc.Servers` to the test server's own address before building the
  router (`routers/legacy.NewRouter(doc)`), so routes match on
  `127.0.0.1:<port>`.
- Run the real server: `server.New(cfg, pool, logger)` over
  `net/http/httptest` on `127.0.0.1`, with a database from `dbtest.New(t)`,
  and `cfg.PublicURL` set to the test server's address.
- kin-openapi has no decoder for `text/html`, which the approval page
  answers. Before any check, call
  `openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.PlainBodyDecoder)`
  once.
- Send every request through one helper. It finds the route, validates the
  request with `openapi3filter.ValidateRequest` (using
  `openapi3filter.NoopAuthenticationFunc`; the server checks proofs itself),
  sends it, then validates the answer with `openapi3filter.ValidateResponse`
  (status, content type, body). Only the two `400` requests, which the test
  makes invalid on purpose, skip the request check; their answers are still
  checked. Every other request must be valid: the `403` requests to
  `startPairing` and `submitCode` carry a valid JSON body with
  `Content-Type: application/json`.
- The helper records each (operation ID, status code) it sees.

**The scenarios.** The test drives the approval API the way the page does,
with four pairings, each started with `startPairing` (201). In order:

| Pairing | Calls, in order |
| --- | --- |
| A | `pollPairing` (200, `waiting_for_approval`); `readApproval` (200, `waiting_for_approval`); `approvalPage` (200); `acceptApproval` (200); `acceptApproval` again (200, `already_decided: true`); `pollPairing` (200, `waiting_for_code`, with `tries_left`); `submitCode` with a wrong code (200, `wrong_code`); `submitCode` with the code from the approval answer (200, `accepted`); `submitCode` again (200, `not_waiting_for_code`); `acknowledgeMachine` with the credential (200, `ok`); `pollPairing` (200, `paired`); `readApproval` (200, `paired`, with `display_name`) |
| B | `acceptApproval`; 5 × `submitCode` with wrong codes (the 5th answers 200, `failed`); `pollPairing` (200, `failed`, with `failure_reason: wrong_codes`) |
| C | `acceptApproval`; `submitCode` with the right code (`accepted`); `reportSaveFailure` with that credential (200, `ok`) |
| D | `rejectApproval` (200, `rejected`) |

Then the error cases:

| Operation | Call |
| --- | --- |
| `health` | 200 |
| `startPairing` | 400: `{"hostname": 5}` |
| `submitCode` | 400: `{}` (no `code`), with pairing A's polling key |
| `pollPairing`, `submitCode` | 401: no `Authorization` header |
| `acknowledgeMachine`, `reportSaveFailure` | 401: an unknown credential |
| `readApproval`, `acceptApproval`, `rejectApproval` | 404: an unknown approval key |
| every operation | 403: `Host: evil.example` |

**The coverage check** runs last: every operation in the file was called,
and every status code the file lists for it was seen, except `500`. A
status listed in the file but never produced fails the test, naming the
operation and status.

### The docs page: `internal/server/apidocs/` (new)

Named `apidocs` so it is not confused with the repository's product `docs/`
folder.

| Route | Answer |
| --- | --- |
| `GET /docs` | `Content-Type: text/html; charset=utf-8`. An HTML page that loads Swagger UI **5.33.0** from `https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/` and shows `/docs/openapi.yaml` |
| `GET /docs/openapi.yaml` | `api.OpenAPI`, with `Content-Type: application/yaml` |

The page loads the two files with integrity hashes, so the browser refuses
them if the CDN ever serves something else:

```html
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui.css"
      integrity="sha384-Ov4/wv3j2bmct8cDc5X4ngJZohVPzEmc6uDPH8WeljUxO5vtoykvMEfbu9Vh6RaW" crossorigin="anonymous">
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui-bundle.js"
        integrity="sha384-YDALVcy8kj8yltLBVi1vBiBAUqdxvus673gM8XKwiy6aDUJFXivF/KCufekjYbVf" crossorigin="anonymous"></script>
```

`apidocs.Register(mux)` adds both routes. `server.New` calls it only when
`cfg.APIDocs` is true. The Host check applies to these routes like any other.

**The setting.** `TERVI_API_DOCS` in `internal/server/config.go`:

| Value | `cfg.APIDocs` |
| --- | --- |
| unset, empty, or `off` | `false` |
| `on` | `true` |
| anything else | `LoadConfig` fails with `TERVI_API_DOCS must be "on" or "off"` |

When it is on, `Run` logs once at start:
`logger.Warn("API docs are on at /docs; turn them off outside development")`.

Add `TERVI_API_DOCS=off` to `.env.example` with a comment. In `README.md`,
under the "How to run it" item task 11 wrote, add: to see the API, start the
server with `TERVI_API_DOCS=on go run ./cmd/server` and open
`http://localhost:8080/docs`.

## Boundaries

- May create or change: `api/api.go`, `api/api_test.go`, `test/contract/`,
  `internal/server/apidocs/`, `internal/server/config.go`, `server.go`, and
  their tests, `.env.example`, `README.md` (the run section), `go.mod`,
  `go.sum`, and this task's row in `slices/0001-pairing/plan.md`.
- Must not change: `api/openapi.yaml`, `internal/server/pairing/`,
  `internal/server/web/`, `internal/server/db/`, `internal/worker/`,
  `internal/secret/`, `cmd/`, `slices/` (except that row), `docs/`,
  `design/`, `AGENTS.md`, `.claude/`, `.codex/`, `scripts/`.
- The only new dependency is `github.com/getkin/kin-openapi v0.149.0` and
  what it needs.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestOpenAPIValid` (`api`) — `api.OpenAPI` loads and `doc.Validate` passes | The contract is valid OpenAPI |
| `TestServerMatchesContract` (`test/contract`) — the scenarios above, each request and answer checked, then the coverage check | The server answers what the contract says |
| `TestContractCatchesExtraField` (`test/contract`) — the helper's answer check, given a hand-made `200` poll answer with an extra field `"credential"`, reports an error | The check really fails on a field the contract does not list |
| `TestDocsOffByDefault` (`internal/server`) — with `APIDocs` false, `GET /docs` and `GET /docs/openapi.yaml` answer `404` | The page is never on by accident |
| `TestDocsOn` (`internal/server`) — with `APIDocs` true, `GET /docs` answers `200` HTML that mentions `swagger-ui-dist@5.33.0` and both `integrity="sha384-…"` values above; `GET /docs/openapi.yaml` answers `200` with exactly `api.OpenAPI` | The page works, pinned |
| `TestAPIDocsSetting` (`internal/server`) — a table test of `TERVI_API_DOCS`: unset, `""`, `off` → false; `on` → true; `yes` → the error | The setting |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes
to `done`.

## Out of scope

- Changing any route's behavior or the contract file.
- The WebSocket contract (`api/asyncapi.yaml`, slice 0002).
- Generating Go code from the contract.

## If anything is unclear

Stop and report the question. Do not guess.
