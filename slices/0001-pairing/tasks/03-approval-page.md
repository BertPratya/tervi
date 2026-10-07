# Task 03 — Approval web page

Slice: 0001-pairing    Risk: low    Depends on: 02

## Goal

The page a user opens from the approval link: it shows the computer's
details, lets the user click Pair or Reject, shows the pairing code, and keeps
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

## Context

### What exists (tasks 01, 02)

- `internal/web`: an empty `Register(mux *http.ServeMux)` that
  `internal/server.New` already calls.
- The approval API (task 02). Proof: `Authorization: Bearer <approval key>`.

| Method and path | Does |
| --- | --- |
| `GET /api/v1/approvals/current` | Current state; changes nothing |
| `POST /api/v1/approvals/current/accept` | The Pair button |
| `POST /api/v1/approvals/current/reject` | The Reject button |

Each answers `200` with
`{"status", "hostname", "os_name", "os_version", "already_decided"}`, plus
`pairing_code` while `waiting_for_code`, `failure_reason` (`wrong_codes`,
`not_saved`, or `not_confirmed`) when `failed`, and `display_name` when
`paired`. An unknown key answers `404 {"error": "invalid_link"}`.

### Files and routes

| File (in `internal/web/static/`) | Purpose |
| --- | --- |
| `index.html` | The page. Loads `<script type="module" src="/static/app.js">`. |
| `app.js` | Reads the key, calls the API, polls, handles clicks, draws the page. Imports `/static/view.js`. |
| `view.js` | One pure function, `view(state)`: from an API answer (or `invalid_link`) to what to show. No browser objects, so it can be tested with Node. |
| `view.test.js` | Tests for `view()`, run with `node --test`. |

Embed the folder with `go:embed`. In `web.Register` add:

- `GET /pair/{approval_key}` → `index.html`, for any key; the page itself
  finds out whether the key is valid.
- `GET /static/` → the embedded files (`index.html` need not be reachable here).

Every response from both routes sets `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`, so the key in the address is not cached or
sent to other sites. No file, font, or script is loaded from another site.

The page reads the key from `window.location.pathname` (the part after
`/pair/`) and sends it only as `Authorization: Bearer <key>`, never in a query
string.

### What the page shows

An empty `hostname`, `os_name`, or `os_version` is shown as `unknown`. `<name>`
below is the hostname (`display_name` when paired).

| Status | The page shows | Polls? |
| --- | --- | --- |
| `waiting_for_approval` | "Pair a computer?", the name, OS name and version, and **Pair** and **Reject** buttons | Yes |
| `waiting_for_code` | "Type this code into the terminal of `<name>`:", the code in large type, and "Never give this code to anyone." | Yes |
| `finishing` | Code accepted. Finishing on `<name>`… | Yes |
| `paired` | ✓ **`<name>` is paired.** `<OS name> <OS version>`. You can close this tab. | No |
| `rejected` | Rejected. This computer was not paired. | No |
| `expired` | ✗ **This link has expired.** Run `tervi pair` on the computer again to get a new link. | No |
| `failed`, `wrong_codes` | ✗ **Pairing failed: the wrong code was typed 5 times.** Run `tervi pair` on the computer again to start over. | No |
| `failed`, `not_saved` | ✗ **Pairing failed: the computer couldn't save its credential.** Check the terminal on `<name>` for details. | No |
| `failed`, `not_confirmed` | ✗ **Pairing failed: the computer didn't confirm in time.** Run `tervi pair` on the computer again. | No |
| `404 invalid_link` | ✗ **Invalid link.** | No |

Every failure uses the same red error style. No status other than `paired`
ever shows "paired" (R27).

### Polling and clicks

While the page shows a status marked "Yes", it repeats
`GET /api/v1/approvals/current` **2 seconds after the previous request ended**
(answer, error, or 10-second timeout), never two at once. It stops as soon as
it shows a status marked "No". A failed request is not shown as an error; the
next poll tries again.

After a click, the page shows the answer's state, whatever `already_decided`
says. Both buttons are disabled while a click's request is running.

### Safety

**Insert every value from the server as text** (`textContent`), never as HTML:
the hostname and OS come from the worker and may contain anything. Do not use
`innerHTML`, `outerHTML`, `insertAdjacentHTML`, or `document.write` anywhere.

## Boundaries

- May create or change: `internal/web/`.
- Must not change: everything else, except this task's row in
  `slices/0001-pairing/plan.md`.
- Do not add Go dependencies. No npm packages.

## Definition of done

| Test | Proves |
| --- | --- |
| `TestPageServed` (Go) — `GET /pair/anything` answers `200` with the HTML, `Cache-Control: no-store`, and `Referrer-Policy: no-referrer` | R6 |
| `TestStaticFilesServed` (Go) — `/static/app.js` and `/static/view.js` answer `200` with a JavaScript content type and the same two headers | The page can load its scripts |
| `TestPageLoadsNothingExternal` (Go) — no embedded file contains `http://` or `https://` | No third-party requests |
| `TestNoHTMLInsertion` (Go) — no embedded file contains `innerHTML`, `outerHTML`, `insertAdjacentHTML`, or `document.write` | The hostname cannot become code |
| `view.test.js` — for every status and failure reason in the table: the right message, buttons only for `waiting_for_approval`, the code only for `waiting_for_code`, and "poll again" only for the three polling statuses | R7, R9, R11, R13, R16, R19, R27 |
| `view.test.js` — empty hostname or OS gives `unknown` | R6 |
| `view.test.js` — `invalid_link` gives "Invalid link." | Unknown keys handled |

`go vet ./...`, `go test ./...`, and `node --test internal/web/` pass. This
task's row in `plan.md` changes to `done`.

## Out of scope

The API endpoints (task 02), the code and credential endpoints (04), the
worker (05, 06, 07), the end-to-end test (08).

## If anything is unclear

Stop and report the question. Do not guess.
