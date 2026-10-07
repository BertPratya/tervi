# Task 04 — Approval web page

Slice: 0001-pairing    Risk: low    Depends on: 03

## Goal

The page a user opens from the approval link: it shows the computer's
details, lets the user accept or reject, shows the pairing code, and keeps
itself up to date until the pairing ends.

## Requirements (copied from the spec)

- R6. When someone opens the approval link, the server shall show the computer
  name and OS, as reported by the worker, with Pair and Reject. Opening the
  link shall not change the pairing.
- R7. When Pair is clicked on a pairing that is waiting for approval, the server
  shall show a pairing code in the browser.
- R9. When Reject is clicked, the browser shall show that the computer was not
  paired, and the worker shall report the rejection within a few seconds and stop.
- R11. When the link is opened again, the page shall show the current state:
  the buttons while waiting for approval, the same code while waiting for the
  code, and otherwise the result (paired, rejected, failed, or expired).
- R13. If a wrong code is typed, then the worker shall show how many tries are
  left. After 5 wrong codes, the pairing shall fail, and both the terminal and
  the browser shall say so. *This task covers the browser side.*
- R16. While a pairing is expired, rejected, or failed, the link shall show only
  the result, and no code shall be accepted.
- R19. When the credential is saved, the worker shall confirm with the server.
  Then the terminal shall show `✓ Paired successfully.`, and the browser shall
  show that the computer is paired, with its name and OS, and that the tab can
  be closed. *This task covers the browser side.*
- R27. Until the worker confirms, the computer shall not appear anywhere as
  paired.

In this plan, the spec's "Pair" button is labelled **Accept**.

## Context

### The API this page uses (task 03)

The page's own address is `/pair/<approval key>`. The page reads the key from
`window.location.pathname` and sends it as `Authorization: Bearer <key>`,
never in a query string and never to another site.

| Method and path | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | Current state; changes nothing |
| `POST /api/v1/approvals/current/accept` | Accept |
| `POST /api/v1/approvals/current/reject` | Reject |

Each answers `200` with:

```json
{
  "status": "...",
  "hostname": "bert-desktop",
  "os_name": "Ubuntu",
  "os_version": "26.04",
  "already_decided": false,
  "pairing_code": "4827-1934",
  "failure_reason": "wrong_codes",
  "display_name": "bert-desktop"
}
```

`pairing_code` appears only for `waiting_for_code`, `failure_reason` only for
`failed`, `display_name` only for `paired`. An unknown key answers
`404 {"error": "invalid_link"}`.

### What the page shows

An empty `hostname`, `os_name`, or `os_version` is shown as `unknown`.
`<name>` below is the hostname (`display_name` when paired).

| Status | The page shows | Polls? |
| --- | --- | --- |
| `waiting_for_approval` | "Pair a computer?", the name, OS name and version, and **Accept** and **Reject** buttons | Yes |
| `waiting_for_code` | "Type this code into the terminal of `<name>`:", the code in large type, and "Never give this code to anyone." | Yes |
| `finishing` | Code accepted. Finishing on `<name>`… | Yes |
| `paired` | ✓ **`<name>` is paired.** `<OS name> <OS version>`. You can close this tab. | No |
| `rejected` | Rejected. This computer was not paired. | No |
| `expired` | ✗ **This link has expired.** Run `tervi pair` on the computer again to get a new link. | No |
| `failed`, `wrong_codes` | ✗ **Pairing failed: the wrong code was typed 5 times.** Run `tervi pair` on the computer again to start over. | No |
| `failed`, `not_saved` | ✗ **Pairing failed: the computer couldn't save its credential.** Check the terminal on `<name>` for details. | No |
| `failed`, `not_confirmed` | ✗ **Pairing failed: the computer didn't confirm in time.** Run `tervi pair` on the computer again. | No |
| `404 invalid_link` | ✗ **Invalid link.** | No |

Every failure uses the same red error style. Never show "paired" for any
status other than `paired` (R27).

### Polling

While the status is `waiting_for_approval`, `waiting_for_code`, or
`finishing`, the page repeats `GET /api/v1/approvals/current` **2 seconds after
the previous request ended** (answer, error, or 10-second timeout). It never
sends two requests at once. It stops polling as soon as it shows a status
marked "No" above.

After a click, the page shows the answer's state, whether or not
`already_decided` is true. Buttons are disabled while a click's request is
running.

### How the page is built

- One HTML file and one JavaScript file in `internal/web/static/`, embedded
  with `go:embed`. Plain JavaScript, no framework, no build step, no file or
  font loaded from another site.
- Put the decision "state → what to show" in one pure function in its own
  module (`view.js`), so it can be tested without a browser.
- **Insert every value from the server as text** (`textContent`), never as
  HTML: the hostname and OS come from the worker and may contain anything.
- `internal/web` serves the page at `GET /pair/{approval_key}` for any key; the
  page itself finds out whether the key is valid. Register the route in
  `cmd/server/main.go`, adding only your own line.
- The page response sets `Cache-Control: no-store` and
  `Referrer-Policy: no-referrer`, so the key in the address is not cached or
  sent to other sites.

## Boundaries

- May create or change: `internal/web/`, `cmd/server/main.go` (one route line).
- Must not change: `internal/pairing/`, `internal/db/`, `internal/secret/`,
  `slices/` except this task's row in `slices/0001-pairing/plan.md`.
- Do not add Go dependencies. No npm packages.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPageServed` (Go) — `GET /pair/anything` answers `200` HTML with `Cache-Control: no-store` and `Referrer-Policy: no-referrer` | R6 |
| `TestPageLoadsNothingExternal` (Go) — the HTML and JavaScript contain no `http://` or `https://` URL | — |
| `TestPageChangesNothing` (Go) — serving the page does not touch the database | R6 |
| `view.test.js` (`node --test`) — for every status and failure reason in the table, `view()` returns the right message, buttons only for `waiting_for_approval`, the code only for `waiting_for_code`, and "poll again" only for the three polling statuses | R7, R9, R11, R13, R16, R19, R27 |
| `view.test.js` — empty hostname or OS gives `unknown` | R6 |
| `view.test.js` — a hostname like `<img src=x onerror=alert(1)>` is returned as plain text, never as HTML | — |

`go vet ./...`, `go test ./...`, and `node --test internal/web/` pass. This
task's row in `plan.md` changes to `done`.

## Out of scope

The API endpoints (task 03, already done), the code and credential endpoints
(05), the worker (06, 07), the full end-to-end test (08).

## If anything is unclear

Stop and report the question. Do not guess.
