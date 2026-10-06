# Workflow walkthrough (draft for discussion)

This file walks through one full round of the workflow, from an approved
`spec.md` to you merging a pull request. Every message passed between roles is
shown in full, and every file that gets written is shown with its location.

It uses the real pairing slice. Two findings in the plan review (step 3) are
**real problems in the approved spec**; they are shown as questions for you and
are not decided here. Everything else (task numbers, pull request numbers,
times) is an example.

Comment on any line you find unclear.

---

## Overview

| Step | Who | Input | Output | Recorded where |
| --- | --- | --- | --- | --- |
| 0 | — | — | Approved `spec.md` | Repository (`main`) |
| 1 | Architect | `spec.md`, `docs/product/`, the code | `plan.md` + task files | Branch `plan/0001-pairing` |
| 2 | Architect → Reviewer | Spec, plan, task files | Plan review report | Architect keeps it for step 4 |
| 3 | Architect | Review findings | Fixed plan, or questions for you | Same branch |
| 4 | Architect → You | Plan + review reports | Pull request for the plan | GitHub: description + one comment per review round |
| 5 | You | The plan pull request | Answers, approval, merge | `main` |
| 6 | Architect → Worker | One task file | Code + tests + worker report | Branch `task/0001-02-...` |
| 7 | Architect → Reviewer | Task file, requirements, diff | Code review report | Architect keeps it for step 9 |
| 8 | Architect → Worker | Findings | Fixes + worker report | Same branch |
| 9 | Architect → Reviewer | Earlier findings, diffs | Round 2 report | Architect keeps it for step 10 |
| 10 | Architect | Worker reports + review reports | Pull request for the task | GitHub: description + one comment per review round |
| 11 | You | The task pull request | Merge, or change requests | `main` |

Rules that apply to every step:

- Only the architect talks to the worker, the reviewer, and you.
- The worker and the reviewer never see each other's words. They see files and diffs.
- The reviewer never changes files.
- Nothing reaches `main` without you.

---

## Step 0 — Starting point

`slices/0001-pairing/spec.md` is merged into `main` with `Status: approved`.
The parts used in this walkthrough:

```markdown
- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting).
- R11. When the link is opened again, the page shall show the current state:
  the buttons while waiting for approval, the same code while waiting for the
  code, and otherwise the result (paired, rejected, failed, or expired).
- R21. Having only the approval link shall not be enough to get the credential.
- R22. The server shall store only hashes of the secrets, never the secrets themselves.
- R24. If the connection to the server is lost while waiting for approval, then
  the worker shall show `Connection lost, retrying...`, keep trying until the
  deadline, and continue normally when the connection returns.
```

---

## Step 1 — Architect writes the plan

**You → Architect** (in chat):

> Plan slice 0001.

The architect creates branch `plan/0001-pairing` from `main` and writes two
kinds of files.

### File 1: `slices/0001-pairing/plan.md`

````markdown
# 0001 Pairing — plan (slice 1)

Status: draft
Spec: [spec.md](spec.md)

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| Server | `cmd/server`, `internal/pairing`, `internal/db` | Go, PostgreSQL |
| Worker command | `cmd/tervi`, `internal/worker` | Go |
| Approval page | `internal/web` | Plain HTML and JavaScript, served by the server |

## Data

Table `pairings`:

| Column | Type | Notes |
| --- | --- | --- |
| id | uuid, primary key | |
| state | text | `waiting_for_approval`, `waiting_for_code`, `finishing`, `paired`, `rejected`, `failed`, `expired` |
| machine_name | text | As reported by the worker |
| os_name | text | As reported by the worker |
| approval_key_hash | bytea, unique | SHA-256 of the approval key |
| worker_secret_hash | bytea, unique | SHA-256 of the worker secret |
| code_hash | bytea, nullable | SHA-256 of the pairing code |
| code_attempts | integer, default 0 | Wrong codes so far |
| credential_hash | bytea, nullable, unique | SHA-256 of the credential |
| created_at | timestamptz | |
| expires_at | timestamptz | `created_at` + 10 minutes |

## Endpoints

| Method and path | Called by | Proof | Purpose |
| --- | --- | --- | --- |
| `POST /api/v1/pairings` | Worker | None | Start a pairing |
| `GET /api/v1/pairings/current` | Worker | Worker secret | Check the state (polling) |
| `POST /api/v1/pairings/current/code` | Worker | Worker secret | Submit the code; get the credential if correct |
| `POST /api/v1/pairings/current/confirm` | Worker | Credential | Confirm the credential was saved |
| `GET /pair/{approval_key}` | Browser | Approval key | The approval page |
| `GET /api/v1/approval` | Browser | Approval key | State shown on the page |
| `POST /api/v1/approval/pair` | Browser | Approval key | Approve; create the code |
| `POST /api/v1/approval/reject` | Browser | Approval key | Reject |

## Worker

- The credential is saved with `github.com/zalando/go-keyring` (Linux Secret Service).
- "Already paired" (R2) is detected from `~/.config/tervi/state.json`, which holds the
  server address and pairing id, never a secret.
- Polling: every 2 seconds.

## Technical decisions

- Secrets are 32 random bytes from `crypto/rand` — impossible to guess.
- Secrets are hashed with SHA-256, not bcrypt — bcrypt exists for weak human
  passwords and is slow on purpose; these secrets are random and strong.
- The pairing code is 8 digits, shown as `4827-1934` — easy to type, and 5 tries
  out of 100 million possibilities cannot be guessed.
- The approval page is plain HTML and JavaScript, not React — it is one small
  page; the React and Theia setup comes later.
- Links use the configured public address (`TERVI_PUBLIC_URL`), never the
  request's `Host` header — a forged header could otherwise point the link at
  another site.

## Requirement coverage

| Requirement | Tasks |
| --- | --- |
| R1–R4 | 06 |
| R5 | 02, 06 |
| R6, R7 | 03, 04 |
| R8 | 06 |
| R9 | 03, 04, 05, 06 |
| R10, R11 | 03, 04 |
| R12 | 06 |
| R13, R14 | 05, 06 |
| R15, R16 | 03, 05, 06 |
| R17 | 05 |
| R18 | 06 |
| R19 | 04, 05, 06 |
| R20 | 06 |
| R21 | 03, 05 |
| R22 | 02, 03, 05 |
| R23, R24 | 06 |

## Tasks

| # | Task | Depends on | Risk | Status |
| --- | --- | --- | --- | --- |
| 01 | Database: `pairings` table, migrations run at server start | — | Normal | todo |
| 02 | Server: start pairing | 01 | Core | todo |
| 03 | Server: approval API (view, pair, reject) | 02 | Core | todo |
| 04 | Approval web page | 03 | Low | todo |
| 05 | Server: worker API (poll, code, confirm) and expiry | 03 | Core | todo |
| 06 | Worker: `tervi pair` command | 05 | Core | todo |
| 07 | End-to-end test of R1–R24 | 04, 06 | Normal | todo |
````

### File 2: one task file per row, for example `slices/0001-pairing/tasks/02-start-pairing.md`

````markdown
# Task 02 — Server: start pairing

Slice: 0001-pairing    Risk: core    Depends on: 01

## Goal

Add `POST /api/v1/pairings`. The worker sends its computer name and OS. The
server creates a pairing that waits for approval, and returns an approval link,
a worker secret, and the expiry time.

## Requirements (copied from the spec)

- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after
  starting). *This task provides the link and the expiry time; task 06 shows them.*
- R22. The server shall store only hashes of the secrets, never the secrets themselves.

## Context

Table `pairings` (created by task 01):

| Column | Type |
| --- | --- |
| id | uuid |
| state | text |
| machine_name | text |
| os_name | text |
| approval_key_hash | bytea, unique |
| worker_secret_hash | bytea, unique |
| created_at | timestamptz |
| expires_at | timestamptz |

Request:

```http
POST /api/v1/pairings
Content-Type: application/json

{"machine_name": "bert-desktop", "os_name": "Ubuntu 26.04"}
```

Response `201 Created`:

```json
{
  "pairing_id": "0b6f...",
  "approval_url": "http://localhost:8080/pair/<approval key>",
  "worker_secret": "<43 characters>",
  "expires_at": "2026-10-06T14:32:00Z"
}
```

Errors: `400` if a name is empty or longer than 255 characters (nothing is
stored); `503` if the database cannot be reached; `500` otherwise.

Secrets: the approval key and the worker secret are each 32 bytes from
`crypto/rand`, encoded as base64url without padding (43 characters). Store the
SHA-256 of each. Never write either one to a log.

The link's address comes from the `TERVI_PUBLIC_URL` environment variable,
never from the request's `Host` header. `expires_at` is the database's current
time plus 10 minutes.

## Boundaries

- May create or change: `internal/pairing/` (new), `cmd/server/main.go` (only to register the route).
- Must not change: `internal/db/migrations/`, other tasks' files, anything in
  `slices/` except this task's row in `plan.md`.
- Do not add dependencies.

## Definition of done

Tests that must exist and pass:

| Test | Proves |
| --- | --- |
| `TestStartPairingReturnsLinkSecretAndExpiry` | R5 (server part) |
| `TestStartPairingStoresOnlyHashes` — the raw approval key and worker secret appear in no column; the stored hashes equal the SHA-256 of the returned values | R22 |
| `TestStartPairingSecretsAreUnique` — two requests never return the same approval key or worker secret | R22 |
| `TestStartPairingRejectsBadInput` — empty or too-long names return 400 and store nothing | — |
| `TestStartPairingIgnoresHostHeader` — a forged `Host` header does not change the link | — |

Also:

- `go vet ./...` and `go test ./...` pass. Tests that need PostgreSQL use the
  database from `compose.yaml`.
- Change this task's row in `plan.md` to `done`.

## Out of scope

Approving and rejecting (task 03), polling and codes (task 05), the worker
command (task 06).

## If anything is unclear

Stop and report the question. Do not guess.
````

Tasks 01 and 03–07 follow the same format.

---

## Step 2 — Plan review, round 1

**Architect → Reviewer** (exact message; the reviewer starts with no memory):

```text
Review the plan for slice 0001-pairing. This is plan review round 1.
Do not change any files.

Read only these files:
- slices/0001-pairing/spec.md
- slices/0001-pairing/plan.md
- every file in slices/0001-pairing/tasks/

Check six things:
1. Coverage: every requirement R1–R24 is handled by at least one task, and
   every test in a task's "Definition of done" is linked to a requirement or
   explained.
2. Conflicts: tasks that can run at the same time do not change the same
   files, and no two documents contradict each other (spec, plan, tasks).
3. Unresolved points: nothing vague ("a few", "some", "etc."), no "TBD", and no
   decision left for the worker to make.
4. Dependencies: the order is possible, there are no loops, and no task uses
   something from a task it does not depend on.
5. Self-contained: a worker could finish each task using only that task file
   and the existing code. It cannot read the spec or the plan.
6. Scope: nothing goes beyond the spec or into "Not in this slice".

Answer in the review report format from AGENTS.md.
For each finding, say whether the architect can fix it alone, or whether it
changes the spec and needs the user.
```

**Reviewer → Architect** (round 1 report):

```markdown
## Plan review round 1 — changes needed

| # | Severity | Where | Check | Finding | Who decides |
|---|----------|-------|-------|---------|-------------|
| 1 | High | spec R21; plan "Endpoints" | Conflict | The approval link is printed in the terminal of whoever runs `tervi pair`. That person can open it, click Pair, read the code, and type it in. So having only the link IS enough to get the credential, which breaks R21. Without sign-in, anyone who can reach the server can pair a computer. | User (spec) |
| 2 | High | spec R11 vs R22 | Conflict | R11 says reopening the link shows the same code again. R22 says the server stores only hashes. A hash cannot be turned back into the code, so the server cannot show it again. | User (spec) |
| 3 | Medium | tasks/06 | Coverage | The plan lists R24 (retry when the connection drops) under task 06, but task 06's "Definition of done" has no test for it. | Architect |
| 4 | Medium | tasks/04, tasks/05 | Conflict | Tasks 04 and 05 can run at the same time (both depend only on 03), and both change `cmd/server/main.go` to register routes. | Architect |
| 5 | Low | tasks/05 | Unresolved | "Retry the confirmation a few times" — how many times, and how long between tries? | Architect |
```

---

## Step 3 — Architect fixes and escalates

The architect fixes findings 3–5 itself:

- **3:** adds `TestPairRetriesWhenConnectionDrops` (R24) to task 06.
- **4:** moves all route registration to task 03; tasks 04 and 05 only add
  handlers in their own folders and no longer touch `cmd/server/main.go`.
- **5:** writes "retry the confirmation 3 times, 2 seconds apart" into task 05.

Findings 1 and 2 change the spec, so the architect does **not** fix them. They
become questions for you (step 4).

**Architect → Reviewer** (round 2):

```text
Review the plan for slice 0001-pairing. This is plan review round 2.
Do not change any files.

Earlier findings (your round 1 report, unchanged):
<the full round 1 table>

Changes since round 1: run
  git diff <round 1 commit>..plan/0001-pairing -- slices/0001-pairing/

Do two things, in this order:
1. For each earlier finding marked "Architect", check the files and mark it
   Fixed, Partly fixed, or Not fixed. Findings marked "User" stay Open.
2. Review all the files again from the start with the same six checks,
   paying extra attention to the lines that changed.

Answer in the review report format from AGENTS.md.
```

**Reviewer → Architect** (round 2 report):

```markdown
## Plan review round 2 — needs user

### Earlier findings
| # | Finding | Status |
|---|---------|--------|
| 1 | Link holder can approve themselves (R21) | Open — needs user |
| 2 | R11 cannot show the code again under R22 | Open — needs user |
| 3 | No test for R24 in task 06 | Fixed |
| 4 | Tasks 04 and 05 both change cmd/server/main.go | Fixed |
| 5 | "A few times" in task 05 | Fixed |

### New findings
None.
```

---

## Step 4 — Architect opens the plan pull request

The architect pushes `plan/0001-pairing` and opens a pull request.

**Pull request title:** `Plan for slice 0001 pairing`

**Pull request description:**

```markdown
## Plan for slice 0001 (pairing)

- `slices/0001-pairing/plan.md`: data, endpoints, technical decisions, coverage, task list
- `slices/0001-pairing/tasks/01-…07-*.md`: 7 task files

### Needs your decision before approval
1. **Anyone with the link can approve (R21).** Options:
   a. Accept it for slice 1 (localhost only), add it to "Not in this slice", and
      make it a requirement that it is solved before slice 4 (real network).
   b. The server owner creates a one-time join token first, and the worker
      needs it to start pairing.
   c. Move sign-in earlier, before slice 4.
2. **R11 vs R22: showing the code again.** Options:
   a. Show the code only once; reopening says "the code was shown on the device
      that approved" (changes R11).
   b. Keep the code encrypted instead of hashed until the pairing ends
      (changes R22's wording).

### Review
Two rounds of plan review; reports are in the comments below.
Findings 3–5 were fixed. Findings 1–2 need you.

### How to check
Read plan.md, then the task files. Look carefully at the "Definition of done"
of core tasks 02, 03, 05, 06 — those tests are what "correct" means.
```

**Comment 1:** the round 1 report, as written by the reviewer.
**Comment 2:** the round 2 report, as written by the reviewer.

The status in `slices/index.md` stays `approved` until the plan is merged.

---

## Step 5 — You answer and approve the plan

1. You answer the two questions in chat or as pull request comments.
2. The architect updates `spec.md` with your decisions, as a separate commit
   in the same pull request, so you see the exact diff of your spec changes.
3. The reviewer runs one more plan review round on the changed files.
4. You read the plan, the task files, and the final report, then merge.
5. The architect changes the slice status in `slices/index.md` to
   `in progress` as part of the same pull request.

---

## Step 6 — Worker builds task 02

Assume task 01 is merged. Task 02 depends only on 01, so it can start.

**Architect** creates the branch `task/0001-02-start-pairing` from `main` in a
separate folder (a worktree), so it does not disturb your folder.

**Architect → Worker** (exact message; the worker starts with no memory):

```text
Build task 02 of slice 0001-pairing.

Work only in this folder: ../tervi-task-0001-02  (branch task/0001-02-start-pairing)

Read:
- slices/0001-pairing/tasks/02-start-pairing.md   (your instructions)
- AGENTS.md                                         (project rules)
- the existing code

Do not read slices/0001-pairing/spec.md, plan.md, or other task files.

Steps:
1. Write the tests listed in "Definition of done" first. Run them and confirm
   they fail for the right reason.
2. Write the code until every test passes.
3. Run go vet ./... and go test ./... and keep the full output.
4. Commit with a message that says what changed, not why you chose it.
5. Stop if anything in the task is unclear. Report the question; do not guess.

Answer with the worker report format from AGENTS.md. Do not open a pull request.
```

**Worker → Architect** (round 1 report):

```markdown
## Worker report — task 02, round 1

Status: done
Branch: task/0001-02-start-pairing
Commits: 3f1a2c4 add start pairing endpoint and tests

### What I built
- internal/pairing/secrets.go: generate a 32-byte secret; SHA-256 hash
- internal/pairing/start.go: POST /api/v1/pairings handler
- internal/pairing/store.go: insert a pairing
- cmd/server/main.go: register the route; read TERVI_PUBLIC_URL

### Tests
| Test | Requirement | Result |
|------|-------------|--------|
| TestStartPairingReturnsLinkSecretAndExpiry | R5 | pass |
| TestStartPairingStoresOnlyHashes | R22 | pass |
| TestStartPairingSecretsAreUnique | R22 | pass |
| TestStartPairingRejectsBadInput | — | pass |
| TestStartPairingIgnoresHostHeader | — | pass |

### Checks run
go vet ./...   → no output
go test ./...  → ok (5 tests in internal/pairing, 1 in cmd/server)

### Decisions I made
- The server refuses to start if TERVI_PUBLIC_URL is missing — links would
  otherwise be wrong.
- Names are trimmed of spaces before the length check.

### Questions
None.
```

The architect keeps this report. The reviewer never sees it.

---

## Step 7 — Code review, round 1

**Architect → Reviewer** (exact message):

```text
Review the code for task 02 of slice 0001-pairing. This is code review round 1.
Do not change any files.

Read:
- slices/0001-pairing/tasks/02-start-pairing.md
- the requirements below
- the changes: git diff main...task/0001-02-start-pairing

Requirements (copied from the spec):
- R5. When pairing starts, the worker shall show the computer name, the OS, an
  approval link, and the clock time the pairing expires (10 minutes after starting).
- R22. The server shall store only hashes of the secrets, never the secrets themselves.

Do not read commit messages, pull requests, or anything else that explains
the change. Judge the code itself.

Run go vet ./... and go test ./... yourself.

Review with two lenses:
A. Spec: is each requirement met? Does each test in "Definition of done"
   exist, and does it really prove what it claims? Was anything added that the
   task did not ask for? Were the boundaries respected?
B. Quality and security: bugs, error handling, secrets in logs or responses,
   input checks, SQL injection.

Answer in the review report format from AGENTS.md.
```

**Reviewer → Architect** (round 1 report):

```markdown
## Code review round 1 — changes needed

Checks I ran: go vet ./... (clean), go test ./... (pass)

### Findings
| # | Severity | Where | Requirement | Finding | Suggested fix |
|---|----------|-------|-------------|---------|---------------|
| 1 | High | internal/pairing/start.go:47 | R22 | When the insert fails, the error log line includes approval_url, which contains the approval key. A secret ends up in the logs. | Log the pairing id only. |
| 2 | Medium | internal/pairing/start_test.go:61 | R22 | TestStartPairingStoresOnlyHashes searches only the approval_key_hash column for the raw key. The key could be stored in another column and the test would still pass, so it does not prove R22. | Read the whole row and check that no column contains either raw secret. |

### Outside this task (not blocking)
- The response has no `Cache-Control: no-store` header, so a proxy could cache
  the worker secret. Worth a requirement in a later task.
```

---

## Step 8 — Worker fixes

**Architect → Worker** (exact message):

```text
Task 02, round 2. The reviewer found these problems:

1. High — internal/pairing/start.go:47 — When the insert fails, the error log
   line includes approval_url, which contains the approval key. Fix: log the
   pairing id only.
2. Medium — internal/pairing/start_test.go:61 — TestStartPairingStoresOnlyHashes
   searches only one column. Fix: check that no column contains either raw secret.

Fix only these. Do not change anything else.
Run go vet ./... and go test ./... again.
Answer with the worker report format from AGENTS.md.
```

The "outside this task" note is **not** sent to the worker. It goes to you in
the pull request, so you can decide whether it becomes a requirement.

**Worker → Architect** (round 2 report):

```markdown
## Worker report — task 02, round 2

Status: done
Commits: 9be0d71 stop logging the approval url; check every column for secrets

### Fixes
| Finding | What I changed |
|---------|----------------|
| 1 | start.go:47 now logs only the pairing id |
| 2 | The test reads the whole row and checks every column for both raw secrets |

### Checks run
go vet ./...   → no output
go test ./...  → ok

### Questions
None.
```

---

## Step 9 — Code review, round 2

**Architect → Reviewer** (exact message):

```text
Review the code for task 02 of slice 0001-pairing. This is code review round 2.
Do not change any files.

Earlier findings (your round 1 report, unchanged):
1. High — internal/pairing/start.go:47 — error log includes the approval key.
2. Medium — start_test.go:61 — test checks only one column.

Changes since round 1: git diff 3f1a2c4..task/0001-02-start-pairing
All changes:            git diff main...task/0001-02-start-pairing

Read the same task file and requirements as in round 1. Do not read commit
messages or anything else that explains the change.

Do two things, in this order:
1. Mark each earlier finding Fixed, Partly fixed, or Not fixed, based on the code.
2. Review all the changes again from the start with both lenses, paying extra
   attention to the lines changed since round 1. Fixes often create new problems.

Run go vet ./... and go test ./... yourself.
Answer in the review report format from AGENTS.md.
```

**Reviewer → Architect** (round 2 report):

```markdown
## Code review round 2 — pass

Checks I ran: go vet ./... (clean), go test ./... (pass)

### Earlier findings
| # | Finding | Status |
|---|---------|--------|
| 1 | Error log includes the approval key | Fixed |
| 2 | Test checks only one column | Fixed |

### New findings
None.

### Outside this task (not blocking)
- Still no `Cache-Control: no-store` header (unchanged from round 1).
```

If round 2 had **not** passed, the architect would send one more round of
fixes. After that, it stops and opens the pull request marked
**"Needs you"**, listing what is still open.

---

## Step 10 — Architect opens the task pull request

The architect pushes the branch and opens a pull request. Its base is `main`,
because task 01 is already merged. (If task 01 were still waiting for you, the
base would be task 01's branch: a stacked pull request.)

**Pull request title:** `Task 02 — Server: start pairing (slice 0001)`

**Pull request description** (built from the worker's reports):

````markdown
## Task 02 — Server: start pairing

Slice: 0001-pairing · Task file: `slices/0001-pairing/tasks/02-start-pairing.md`
Covers: R5 (server part), R22 · Risk: core — please review line by line

### What was built
- `internal/pairing/secrets.go`: 32-byte random secrets and their SHA-256 hashes
- `internal/pairing/start.go`: `POST /api/v1/pairings`
- `internal/pairing/store.go`: saving a pairing
- `cmd/server/main.go`: route registration; reads `TERVI_PUBLIC_URL`

### Tests
| Test | Proves |
|------|--------|
| TestStartPairingReturnsLinkSecretAndExpiry | R5 |
| TestStartPairingStoresOnlyHashes | R22 |
| TestStartPairingSecretsAreUnique | R22 |
| TestStartPairingRejectsBadInput | input checks |
| TestStartPairingIgnoresHostHeader | link address can't be forged |

### Decisions the worker made — please check
- The server refuses to start without `TERVI_PUBLIC_URL` — links would otherwise be wrong.
- Names are trimmed of spaces before the length check.

### For you to decide (from the reviewer, not blocking)
- Add `Cache-Control: no-store` to responses that contain secrets? If yes, it
  becomes a requirement in a later task.

### How to check it yourself
```bash
docker compose up -d
TERVI_PUBLIC_URL=http://localhost:8080 go run ./cmd/server
curl -i -X POST localhost:8080/api/v1/pairings \
  -H 'Content-Type: application/json' \
  -d '{"machine_name":"test","os_name":"linux"}'
go test ./internal/pairing/...
```

### Review history
2 rounds; reports in the comments below. Round 1 found 2 problems; both were fixed.
````

**Comment 1:** the code review round 1 report, as written by the reviewer.
**Comment 2:** the code review round 2 report, as written by the reviewer.

The worker's commit already changed task 02's row in `plan.md` to `done`, so
merging the pull request is what makes that line true on `main`.

---

## Step 11 — You review and merge

In the evening you open the pull request.

1. **Read the description**: what was built, which requirements, and the
   decisions the worker made.
2. **Read the review comments**: what went wrong first and how it was fixed.
3. **Predict**: before opening "Files changed", guess which files changed and
   roughly how.
4. **Run**: the commands under "How to check it yourself".
5. **Investigate**: follow one request through the code, from the route to the
   database. Ask the architect "why" about anything unclear, then explain the
   flow back in your own words.
6. **Modify**: make one small change yourself, for example add a test case.
7. **Decide**:
   - **Merge**, or
   - **Request changes**: write comments on the lines. Then tell the architect
     "PR #4 has comments". The architect sends your comments to the worker as
     findings, the reviewer runs another round, the architect adds that round's
     report as a new comment, and you review again.
   - Also answer anything under "For you to decide".

After you merge, the architect updates its local `main` and starts the next
task whose dependencies are all merged.

---

## Appendix A — Draft `AGENTS.md` (read by every agent)

````markdown
# AGENTS.md

## Project
tervi: a Go server and a Go worker command. PostgreSQL via compose.yaml.

## Commands
```bash
docker compose up -d     # start PostgreSQL
go vet ./...
go test ./...
```

## Rules
- Never commit to main. Never merge a pull request.
- Change only the files your task allows.
- If anything is unclear, stop and report the question. Do not guess.
- Never write a secret to a log, an error message, or a test failure message.
- Commit messages say what changed, not why you chose it.

## Worker report format
## Worker report — task NN, round N
Status: done | blocked
Branch / Commits
### What I built (round 1) or ### Fixes (later rounds)
### Tests (test → requirement → result)
### Checks run (command → result)
### Decisions I made
### Questions

## Review report format
## <Plan|Code> review round N — pass | changes needed | needs user
Checks I ran
### Earlier findings (round 2 onwards): # · finding · Fixed | Partly fixed | Not fixed | Open
### Findings: # · severity · where · requirement · finding · suggested fix (plan reviews: · who decides)
### Outside this task (not blocking)
````

## Appendix B — Draft sub-agent definitions

`.claude/agents/worker.md`:

```markdown
---
name: worker
description: Builds exactly one task from a task file. Tests first, then code.
tools: Read, Write, Edit, Bash, Grep, Glob
---
You build one task. Your instructions are the task file the architect names.
Read only that task file, AGENTS.md, and the existing code. Never read the
spec, the plan, or other task files. Follow AGENTS.md. Report with the worker
report format. Never open a pull request or merge.
```

`.claude/agents/reviewer.md`:

```markdown
---
name: reviewer
description: Independently reviews a plan or code against requirements. Never changes files.
tools: Read, Bash, Grep, Glob
---
You review; you never change files. Read only what the architect lists. Never
read commit messages, pull requests, or any explanation of why a change was
made; judge the files themselves. Report with the review report format in
AGENTS.md.
```

The reviewer has no `Write` or `Edit` tool, so it cannot change files even by
mistake.
