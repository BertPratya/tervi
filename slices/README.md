# Slices

A slice is one thin piece of a feature that works end to end and can be
demonstrated. Each slice lives in its own folder. [index.md](index.md) lists
every slice and its status. The workflow and the roles are in
[`AGENTS.md`](../AGENTS.md); this file holds the formats.

## Folder layout

```text
slices/
  index.md                 # one row per slice
  backlog.md               # deferred work
  NNNN-name/
    spec.md                # what to build. Written by the user.
    plan.md                # how to build it, and the task list. Drafted by the architect, approved by the user.
    tasks/
      NN-name.md           # one file per task and pull request. Drafted by the architect, approved by the user.
```

`NNNN` is a four-digit slice number and `NN` a two-digit task number, both in
creation order: `0001-pairing`, `tasks/02-start-pairing.md`.

## Branches

| Branch | Holds |
| --- | --- |
| `slice/NNNN-name` | The spec |
| `plan/NNNN-name` | The plan and task files |
| `task/NNNN-NN-name` | One task's code |

## Who reads what

| Role | Reads | Writes |
| --- | --- | --- |
| User | Everything | `spec.md`; decisions; approves everything else |
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
| done | Merged. The task's own pull request changes its row to `done`, so the row is true once merged. |

Work in progress is visible as open pull requests on GitHub, not in this table.

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
could finish it. After approval, the instructions do not change; reports and
reviews go on the pull request.

## backlog.md

```markdown
# Backlog

| # | Item | Why later | Found in | Status |
| --- | --- | --- | --- | --- |
| B1 | <what to do> | <reason> | <PR #N, review round N> | open |
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

Read only: slices/NNNN-name/spec.md, slices/NNNN-name/plan.md, and every file
in slices/NNNN-name/tasks/.

Answer in the review report format from slices/README.md.
```

## Code review message (architect → reviewer)

```text
Review the code for task NN of slice NNNN-name. This is code review round N.

Worktree: <absolute path>
Task file: slices/NNNN-name/tasks/NN-name.md
Changes: git diff <base>...task/NNNN-NN-name

Requirements (copied from the spec):
- RN. <full text>

Answer in the review report format from slices/README.md.
```

From round 2, the message adds the earlier findings (the reviewer's own table,
unchanged) and the command for the changes since the last round:
`git diff <last reviewed commit>..task/NNNN-NN-name`. The worker's reports are
never included.

## Review report (reviewer → architect)

```markdown
## <Plan | Code> review round N — pass | changes needed | needs user

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

- `plan.md`: <one line>
- `tasks/`: <number> task files

### Needs your decision
<numbered questions with lettered options and the architect's suggestion; or "None">

### Review
<number> rounds of plan review; reports are in the comments.

### How to check
Read plan.md, then the task files. Check the "Definition of done" of every core task.
```

## Task pull request

Title: `Task NN — <name> (slice NNNN)`

````markdown
## Task NN — <name>

Slice: NNNN-name · Task file: `slices/NNNN-name/tasks/NN-name.md`
Covers: RN, RN · Risk: <risk> · Stacked on: <#PR, or main>

### What was built
- `<path>`: <one line>

### Tests
| Test | Proves |

### Decisions the worker made — please check
- <decision> — <why>

### For you to decide (not blocking)
<"outside this task" notes from the reviewer; or "None">

### How to check it yourself
```bash
<commands>
```

### Review history
<number> rounds; reports are in the comments.
````

When the review did not pass after 2 fix rounds, the title ends with
`(needs you)` and the description starts with the open findings.

## Decision pull request

A draft pull request. Title: `Task NN — <name> (needs your decision)`

```markdown
## Needs your decision
<the question>

Options:
a. <option> — <consequence>
b. <option> — <consequence>

Architect's suggestion: <option> — <why>

### Waiting on this
Tasks <numbers> wait. Independent tasks continue.
```

## Review comment on a pull request

One comment per review round: the reviewer's report, unchanged. The architect
adds them in round order.

---

## Status values in `index.md`

| Status | Meaning |
| --- | --- |
| draft | The spec is being written |
| approved | The spec is approved; planning can start |
| in progress | The plan is merged; tasks are being built and merged |
| done | Every requirement is met and the slice was demonstrated end to end |
