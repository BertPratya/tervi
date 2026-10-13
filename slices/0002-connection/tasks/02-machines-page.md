# Task 02 — Server: the machines list and the Machines page

Slice: 0002-connection    Risk: normal    Depends on: 01, 08

## Goal

Show every paired machine at `/machines`, Online or Offline, with "last seen"
for offline machines, refreshing every 5 seconds.

## Requirements (copied from the spec)

- R21. The server shall show `/machines`: every paired machine (only those
  whose pairing finished), sorted by name, with its name, OS, and `Online` or
  `Offline`. An
  offline machine shall also show `last seen <time ago>`, or `never connected`
  if it never connected.
- R22. The page shall refresh by itself every 5 seconds.
- R23. A machine shall show as Online exactly while the server has its
  connection open. After the server restarts, every machine shall show as
  Offline until it connects again. *This task: the list and the page read
  the in-memory list from task 01.*
- R24. The server shall record when it last heard from each machine: when it
  connects, at least once a minute while connected, and when the connection
  ends (the time of the last message received). "Last seen" may be up to one
  minute early if the server itself stopped suddenly, or if the machine had
  stopped answering when the server stopped. *This task: showing
  it.*
- R25. The credential shall never appear in terminal output, logs, the
  Machines page, or `state.json`. *This task: the Machines page and its
  data.*
- R27. **Known limitation (as in slice 1):** without sign-in, anyone who can
  reach the server can see the Machines page. The server still listens on
  `localhost` only.
- R34. When the server answers `internal_error`, it shall log what kind of
  operation failed, and never the error's details, a request value, a key,
  a credential, or a hash. *This task: the machines list's database
  failure, through task 08's helper.*

## Context

### What exists (tasks 01 and 08)

`internal/server/connection` keeps the in-memory list of open connections
(`*connection.List`) and exports `Online(machineID string) bool` and
`OnlineIDs() map[string]bool`. `server.New(cfg, pool, logger, connections)`
receives the list. `machines.last_seen_at` exists (migration `0002`).
`internal/server/httpapi` has `WriteJSON`, `WriteError`, and
`WriteInternalError(w, logger, kind, err)`; use them for every answer, with
the kind `"list machines"` for a database failure.

### The list: `internal/server/machines`

`Register(mux, Deps)` adds `GET /api/v1/machines`. No proof (R27). The
answer is described in `api/openapi.yaml` (`MachineList`, `Machine`):

```json
{
  "machines": [
    { "machine_id": "…", "display_name": "bert-desktop", "os_name": "Ubuntu",
      "os_version": "26.04", "online": false, "last_seen_seconds_ago": 75 }
  ]
}
```

- Only machines with `status = 'active'`, sorted by `display_name`, then by
  `machine_id` for equal names.
- `online` comes from the in-memory list, never from the database.
- `last_seen_seconds_ago` is whole seconds between the database's `now()`
  and `last_seen_at` (rounded down, never below 0), or `null` when
  `last_seen_at` is null. Compute it in SQL so server and database clocks
  cannot disagree.
- A database failure answers `500 {"error": "internal_error"}` through
  `httpapi.WriteInternalError`, which logs the kind (R34).
- Never select or return `credential_hash`.

### The page: `internal/server/web`

`GET /machines` answers an HTML page with the same headers as the approval
page (`Cache-Control: no-store`, `Referrer-Policy: no-referrer`). Add
`static/machines.html` and `static/machines.js`, plus a pure view function in
`static/machines-view.js` tested by `static/machines-view.test.js` with
`node --test` (same pattern as `view.js` and `view.test.js`). Serve the two
scripts under `/static/` like `app.js`; the test file answers `404`.

The page reads `/api/v1/machines` when it opens and every 5 seconds after,
counted from the end of the previous request. `machines-view.js` exports the
wait as `REFRESH_MS = 5000`, and `machines.js` uses that constant. It shows a heading `Machines`
and one row per machine:

| Machine | Row |
| --- | --- |
| Online | `● <display_name>   <os_name> <os_version>   Online` |
| Offline, seen | `○ <display_name>   <os_name> <os_version>   Offline · last seen <time ago>` |
| Offline, never seen | `○ <display_name>   <os_name> <os_version>   Offline · never connected` |

`<time ago>` from `last_seen_seconds_ago`:

| Seconds | Text |
| --- | --- |
| under 60 | `less than a minute ago` |
| under 3600 | `1 minute ago`, `N minutes ago` |
| under 86400 | `1 hour ago`, `N hours ago` |
| otherwise | `1 day ago`, `N days ago` |

With no machines: `No machines yet. Pair one with: tervi pair --server <this page's address>`.
When a request fails: keep the last list and show `Can't reach the server. Trying again…`
above it until a request succeeds. Names come from the computer, so insert
them as text, never as HTML.

Screen readers (decided by the user during the slice review): only the
`Can't reach the server. Trying again…` line is announced (`aria-live`
on that line alone), so a screen reader does not read the whole list aloud
on every 5-second refresh. That line is one element written in
`machines.html`, outside the part each refresh rebuilds; a refresh only sets
its text (the failure message, or empty). A live region created together
with its text is often not announced at all (added in fix 2).

### Contract

Remove `x-planned: true` from `listMachines` and `machinesPage` in
`api/openapi.yaml`. Call both in `test/contract/contract_test.go` so they
return `200` and `403`, with at least one machine in the answer.

## Boundaries

- May create or change: `internal/server/machines/`, `internal/server/web/`
  (Go and `static/`), `internal/server/server.go` (register the new routes),
  their tests, `test/contract/contract_test.go`, `api/openapi.yaml` (only the
  two `x-planned` lines).
- Must not change: `internal/server/connection/` and
  `internal/server/httpapi/` (use their exported functions),
  `internal/protocol/`, anything under `internal/worker/`,
  `design/`, `slices/` except this task's row in `plan.md`.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestListOnlyActiveSorted` — only `active` machines, sorted by name then id | R21 |
| `TestListOnlineFromMemory` — `online` is true exactly for machines in the in-memory list | R23 |
| `TestListLastSeen` — seconds since `last_seen_at`, and `null` when never connected | R21, R24 |
| `TestListHasNoCredential` — the answer has no credential or hash | R25 |
| `TestListDatabaseFailure` — a database failure answers `500 internal_error`, and the server log has the kind `list machines` | R34 |
| `TestMachinesPageServed` — `/machines` and its scripts answer with the right types and headers; the test file answers `404` | R21 |
| `TestMachinesPageAnnouncesOnlyFailureLineAndPreservesRowSpacing` — one failure line, already on the page and outside the part that refreshes, is the only `aria-live` region; rows keep their spacing (added in fix 1, changed in fix 2) | R21, R22 |
| `machines-view.test.js` — each row form, every `<time ago>` boundary (59, 60, 3599, 3600, 86399, 86400), the empty state, the failure line, and that a name with `<b>` is shown as text | R21, R22 |
| `machines-view.test.js` — `REFRESH_MS` is `5000` | R22 |
| Contract test — `listMachines` and `machinesPage` return `200` and `403`; no `x-planned` left on them | contract |

`go vet ./...`, `go test ./...`, and `node --test internal/server/web/static/`
pass. This task's row in `plan.md` changes to `done`.

## Out of scope

- The connection itself (task 01).
- Showing machines on the worker side (tasks 03–06).

## If anything is unclear

Stop and report the question. Do not guess.
