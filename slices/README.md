# Slices

A slice is one thin piece of a feature that works end to end and can be
demonstrated. Each slice lives in its own folder. [index.md](index.md) lists
every slice and its status. The workflow and the roles are in
[`AGENTS.md`](../AGENTS.md); this file holds the formats.

## Folder layout

```text
slices/
  index.md                 # one row per slice
  roadmap.md               # the expected order of slices, and what each lets the user do
  backlog.md               # deferred work
  NNNN-name/
    spec.md                # what to build. Written by the user.
    plan.md                # how to build it, and the task list. Written by the architect with the user, who chooses every design decision.
    checklist.md           # how the user tests the slice, and the results. Drafted by the architect, filled in by the user.
    tasks/
      NN-name.md           # one file per task. Drafted by the architect, checked by the reviewer. The user does not read them.
```

`NNNN` is a four-digit slice number and `NN` a two-digit task number, both in
creation order: `0001-pairing`, `tasks/02-start-pairing.md`.

## Branches

| Branch | Holds | Merged into |
| --- | --- | --- |
| `plan/NNNN-name` | The spec, plan, task files, and contract changes | `main`, by the user (plan pull request) |
| `slice/NNNN-name` | The built slice: every task and fix | `main`, by the user (slice pull request) |
| `task/NNNN-NN-name` | One task's code | `slice/NNNN-name`, by the architect |
| `fix/NNNN-N` | One fix after its task is in | `slice/NNNN-name`, by the architect |

## Who reads what

| Role | Reads | Writes |
| --- | --- | --- |
| User | `spec.md`, `plan.md`, `checklist.md`, the contracts (`api/`, `design/`), and the running slice. Not task files or code | `spec.md`; chooses every design decision, which the architect writes into `plan.md`; answers questions; the hand-check results in `checklist.md`; approves the plan and the slice |
| Architect | Everything | `plan.md`, task files, `backlog.md`, `index.md`, commits, pull requests and their comments |
| Worker | Its worker message, its task file, the code, `AGENTS.md`, `api/`, `design/` | Code and tests, in its worktree |
| Reviewer | Only what the review message lists | Nothing; it reports to the architect |

The worker never reads `spec.md` or `plan.md`. Each task file copies in the
requirements and context it needs.

---

## spec.md

Describe only what can be noticed from outside the code: by the user, an
attacker, or another computer. Tables, libraries, and endpoints belong in
`plan.md`.

```markdown
# NNNN Name — slice N

Status: draft | approved

## What the user does
Numbered steps, from start to finish.

## Whole design (all slices, short)
The full lifecycle this slice is part of, including later slices.

## Must always be true (this slice)
- R1. The system shall ...
- R2. When <event>, the system shall ...
- R3. While <state>, the system shall ...
- R4. If <unwanted condition>, then the system shall ...

## Not in this slice
What is left for later slices.

## Open questions
Undecided points. Must be empty before the spec is approved.

## Decisions
- <decision> — <one-line why>
```

Each requirement has an ID (`R1`, `R2`, ...) and becomes at least one test.
Task files and pull requests refer to requirements by ID.

## plan.md

```markdown
# NNNN Name — plan

Status: draft | approved
Spec: [spec.md](spec.md)

## Overview
| Part | Location | Notes |

## Data
What this slice adds or changes in design/data-model.md (changed in the same
pull request), with a link. Not a copy.

## Interfaces
What this slice adds or changes in api/openapi.yaml (changed in the same pull
request): a table of operations, who calls them, with what proof, for what
purpose. Request and answer details live only in the contract. Worker
commands, which are not HTTP, are described here in full.

## Mechanisms
Methods that apply to several steps (expiry, retries, hashing, ...). For each:
what it applies to, which requirements it covers, and how it works.

## Technical decisions
- <decision> — <one-line why>
  Not chosen: <option> — <why not>; <option> — <why not>
  (or "Not chosen: none — <why no other option is realistic>")
(Every design decision, as defined in AGENTS.md, is here, chosen by the
user. A task file repeats what its task needs from here.)

## Requirement coverage
| Requirement | Tasks |
(every requirement in the spec appears here)

## Tasks
| # | Task | Depends on | Risk | Status |
```

Risk is `low`, `normal`, or `core`. Core tasks touch secrets, state changes,
retries, or anything a requirement calls security.

Task status:

| Status | Meaning |
| --- | --- |
| todo | Not started |
| done | Merged into the slice branch. The task's own branch changes its row to `done`, so the row is true once merged. |

Work in progress is visible on the slice pull request, not in this table.

## Task file: `tasks/NN-name.md`

```markdown
# Task NN — <name>

Slice: NNNN-name    Risk: low | normal | core    Depends on: <task numbers or —>

## Goal
What this task delivers, in two or three sentences.

## Requirements (copied from the spec)
- RN. <full text, copied word for word>

## Context
Everything the worker needs that is not in the code yet: table shapes,
request and response examples, error codes, configuration, exact values.

## Boundaries
- May create or change: <paths>
- Must not change: <paths>

## Definition of done
| Test | Proves |
| --- | --- |
| `TestName` — <what it checks> | RN |

`go vet ./...` and `go test ./...` pass. This task's row in `plan.md` changes to `done`.

## Out of scope
What belongs to other tasks, with their numbers.

## If anything is unclear
Stop and report the question. Do not guess.
```

A task file is complete when a worker holding only this file and the code
could finish it. After approval, the instructions change only to add a
decision the user made during building; reports and reviews go on the slice
pull request.

## checklist.md

How the user tests the slice before merging it, and the record that every
check passed.

```markdown
# NNNN Name — checklist

Spec: [spec.md](spec.md)
Tried on: <date> · Commit: <short commit id>

## Before you start
<the commands to run first, and what each terminal or browser tab is for>

## Check by hand
Do the rows in order: each one can depend on the state the row before left.

| # | Requirement | Do this | You should see | Result |
| --- | --- | --- | --- | --- |
| 1 | RN | <one action> | <exactly what appears, quoting messages> | ⬜ |

## Proved by tests
What hand checks cannot cover: things that take minutes to happen, failures
that are hard to cause, or what only the database or the code can show.

| Requirement | What the tests prove | Tests | Result |
| --- | --- | --- | --- |
| RN | <one line> | `TestName`, `TestName` | ⬜ |

Tests run: `go test ./...` on <commit> → <result>
```

- **Every requirement appears in at least one part.** A requirement that hand
  checks cover only partly appears in both, and the "What the tests prove"
  column says which part the tests cover.
- **Result** is `⬜` (not done), `✅` (pass), or `❌ fix N` (failed; see fix N
  on the slice pull request). After the fix, the row is checked again and
  becomes `✅`.
- Each test named in "Proved by tests" is in a task's "Definition of done".
- The user fills in "Check by hand". The architect fills in "Proved by tests"
  from a test run on the same commit.

## backlog.md

```markdown
# Backlog

| # | Item | Why later | Found in | Status |
| --- | --- | --- | --- | --- |
| B1 | <what to do> | <reason> | <PR #N, task NN review round N> | open |
```

Status is `open`, `planned → NNNN` (included in that slice's spec), or `done`.
The architect removes `done` rows when their slice is done.

---

## Worker message (architect → worker)

```text
Build task NN of slice NNNN-name. This is round N.

Worktree: <absolute path>    Branch: task/NNNN-NN-name

Read: slices/NNNN-name/tasks/NN-name.md, AGENTS.md, and the existing code.
The task file is your full instructions; skip the spec, the plan, and other
task files.

1. Write the tests from "Definition of done" first. Run them and confirm they
   fail for the right reason.
2. Write the code until every test passes.
3. Run go vet ./... and go test ./... and keep the output.
4. Leave committing to the architect. Suggest a commit message.
5. If anything in the task is unclear, stop and ask. Do not guess.

Answer in the worker report format from slices/README.md.
```

In later rounds, the message lists the findings to fix, word for word from the
reviewer or the user, and ends with: "Fix only these. Leave everything else
as it is."

A fix after its task is in (branch `fix/NNNN-N`) uses the same message. It
names the task file the fix belongs to, or every task file it touches, and
lists the findings from the start.

## Worker report (worker → architect)

```markdown
## Worker report — task NN, round N

Status: done | blocked
Suggested commit message: <what changed>

### What I built            (round 1)
### Fixes                   (later rounds: finding → what changed)
### Tests
| Test | Requirement | Result |
### Checks run
<command> → <result>
### Decisions I made
- <decision> — <why>
### Questions
<none, or the question that blocked the work>
```

## Plan review message (architect → reviewer)

```text
Review the plan for slice NNNN-name. This is plan review round N.

Read only: AGENTS.md, slices/NNNN-name/spec.md, slices/NNNN-name/plan.md,
slices/NNNN-name/checklist.md, and every file in slices/NNNN-name/tasks/.

Answer in the review report format from slices/README.md.
```

## Code review message (architect → reviewer)

```text
Review the code for task NN of slice NNNN-name. This is code review round N.

Worktree: <absolute path>
Task file: slices/NNNN-name/tasks/NN-name.md
Changes: git diff slice/NNNN-name...task/NNNN-NN-name

Requirements (copied from the spec):
- RN. <full text>

Answer in the review report format from slices/README.md.
```

From round 2, the message adds the earlier findings (the reviewer's own table,
unchanged) and the command for the changes since the last round:
`git diff <last reviewed commit>..task/NNNN-NN-name`. The worker's reports are
never included. A fix branch is reviewed the same way, with `fix/NNNN-N` in
place of the task branch.

## Slice review message (architect → reviewer)

```text
Review the whole of slice NNNN-name. This is slice review round N.

Worktree: <absolute path, checked out on slice/NNNN-name>
Read: slices/NNNN-name/spec.md, slices/NNNN-name/plan.md, every file in
slices/NNNN-name/tasks/, api/, and design/data-model.md.
Changes: git diff main...slice/NNNN-name

Each task already passed its own review. Look for what only the whole slice
shows.

Answer in the review report format from slices/README.md.
```

From round 2, it adds the earlier findings and the changes since the last
round, as for a code review.

## Review report (reviewer → architect)

```markdown
## <Plan | Code | Slice> review round N — pass | changes needed | needs user

Checks run: <command> → <result>

### Earlier findings          (round 2 onwards)
| # | Finding | Status |
(Fixed | Partly fixed | Not fixed | Open)

### Findings
| # | Severity | Where | Requirement | Finding | Suggested fix | Who decides |
(None, if there are none)

### Outside this task (not blocking)
- <item>
```

---

## Plan pull request

Title: `Plan for slice NNNN <name>`

```markdown
## Plan for slice NNNN (<name>)

- `spec.md`: approved in chat
- `plan.md`: <one line>
- `tasks/`: <number> task files
- Contracts: <what changed in api/ and design/data-model.md; or "None">

### Needs your decision
<numbered questions with lettered options and the architect's suggestion; or "None">

### Review
<number> rounds of plan review; reports are in the comments.

### How to check
Read spec.md and plan.md. Then read checklist.md: is any requirement missing,
or any check you would want to do yourself? Then read the contract changes.
The reviewer checked the task files; you don't need to read them.
```

## Slice pull request

From `slice/NNNN-name` into `main`. It opens as a draft after the first task
is in, and the architect updates the description as tasks land.

Title: `Slice NNNN — <name>`

````markdown
## Slice NNNN — <name>

Spec: `slices/NNNN-name/spec.md` · Plan: `slices/NNNN-name/plan.md`

### Tasks
| # | Task | Covers | Risk | Status |
(Status: todo | building | in | stopped: needs you)

### What you can try now
```bash
<commands, for what is in so far>
```

### Needs your decision
<open questions; or "None">

### Decisions the worker made — please check
- Task NN: <decision> — <why>

### For you to decide (not blocking)
<"outside this task" notes from the reviewer; or "None">

### Review history
One comment per task, fix, and slice review round.
````

When the slice is ready, the architect marks the pull request ready and adds
"Slice review: passed in <number> rounds" and "Tried end to end: <date>".

## Task comment on the slice pull request

One comment when a task or a fix is merged into the slice branch.

```markdown
### Task NN — <name> is in   (or: Fix N — <one line> is in)

Task file: `slices/NNNN-name/tasks/NN-name.md` · Covers: RN, RN · Risk: <risk>

- `<path>`: <one line>

| Test | Proves |

<number> review rounds. Reports below.
```

The reviewer's reports follow in the same comment, one per round, unchanged,
in round order.

## Decision comment on the slice pull request

```markdown
## Needs your decision — task NN
<the question>

Options:
a. <option> — <consequence>
b. <option> — <consequence>

Architect's suggestion: <option> — <why>

### Waiting on this
Tasks <numbers> wait. Independent tasks continue.
```

## Review comment on the plan pull request

One comment per plan review round: the reviewer's report, unchanged. The
architect adds them in round order.

---

## Status values in `index.md`

| Status | Meaning |
| --- | --- |
| draft | The spec is being written |
| approved | The spec is approved; planning can start |
| in progress | The plan is merged; tasks are being built on the slice branch |
| done | Every row of `checklist.md` passed. Set on the slice branch, so it is true once the slice pull request is merged. |
