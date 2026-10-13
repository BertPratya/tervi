# AGENTS.md

tervi lets a user run coding agents on their own computers from a browser. It
has a Go server (`cmd/server`), a Go worker command (`cmd/tervi`), and
PostgreSQL.

## Commands

```bash
docker compose up -d           # local PostgreSQL
go run ./cmd/server migrate    # apply database migrations (the server never does)
go run ./cmd/server            # start the server
go vet ./...
go test ./...
```

Inside the Codex sandbox the default Go build cache is read-only. Prefix Go
commands with `GOCACHE=/tmp/<worktree folder name>-gocache`.

The sandbox blocks the network unless `.codex/config.toml` allows it. That file
allows it, so tests can reach the local PostgreSQL and Go can download modules.
Start PostgreSQL from outside the sandbox: the architect runs
`docker compose up -d` before starting a worker.

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

- **Name each package after the feature it provides**: `pairing`,
  `credential`, `launcher`. Generic names (`local`, `flow`, `util`, `common`,
  `helpers`) are not allowed.
- **One file per topic** inside a package, named after it: one endpoint or
  group of endpoints, one job, one data type. The package's main file holds
  its types and the helpers its files share.
- **Split a file over 300 lines** (tests not counted) by topic. The same SQL
  or logic written twice becomes one shared constant or function.
- A plan's Overview names the package for every part; check it against
  these rules before the plan is approved.

## Contracts and data model

These files hold the design that outlives any one slice. All are readable by
people and by agents:

| File | Holds |
| --- | --- |
| [`api/openapi.yaml`](api/openapi.yaml) | Every API route, the web pages, and `/health`: who calls it, with what proof, every request and answer. Static files and the development-only docs page are not listed. |
| [`api/asyncapi.yaml`](api/asyncapi.yaml) | Every message sent over a machine's connection (the WebSocket), the checks, the timeout, and the close codes |
| [`design/data-model.md`](design/data-model.md) | Every database table and column, each lifecycle, and the worker's local data |

- **A plan pull request changes them directly**, in the same pull request as
  `plan.md`. The plan links to what changed instead of copying it. Each
  operation it adds to `api/openapi.yaml` is marked `x-planned: true`, so
  the contract test passes before the code exists; the task that builds the
  operation removes the mark.
- **The code must match them.** A test checks the server against
  `api/openapi.yaml`, and tests check both sides against
  `api/asyncapi.yaml`. For the database, the migration files are the truth and
  `design/data-model.md` must agree with them.
- A task that finds the contract wrong stops and reports it; it does not
  change the code to disagree with the contract.

## Roles

| Role | Who | Job |
| --- | --- | --- |
| User | The human owner | Writes specs, makes every design decision, answers every question brought to them, reviews and merges every pull request |
| Architect | The main Claude Code session | Writes the plan with the user (the user makes every design decision), dispatches work, commits, merges finished tasks into the slice branch, opens pull requests, records every review. The only role that talks to the others. |
| Worker | Codex (`gpt-6-luna`, effort `xhigh`) through the Codex plugin, launched by `scripts/run-worker.sh` | Builds one task inside its worktree |
| Reviewer | `.claude/agents/reviewer.md` (`claude-opus-5-5`, effort `high`) | Reviews a plan, a task, or a whole slice. Read-only. |

The worker and the reviewer see files and diffs only, never each other's words.

Formats for every file, message, report, and pull request are in
[`slices/README.md`](slices/README.md).

## Workflow: a slice

A slice reaches `main` through two pull requests: the **plan pull request**
(what to build) and the **slice pull request** (the built slice). Tasks have no
pull requests of their own.

1. **Spec.** The user writes `slices/NNNN-name/spec.md` on branch
   `plan/NNNN-name` while the architect asks questions. The user approves it
   in chat.
2. **Plan.** On the same branch, in two parts, in this order:
   1. **`plan.md`, with the user.** The architect and the user go through
      `plan.md` in chat, one section at a time, in the order of the format
      in `slices/README.md`, from "Overview" through "Technical decisions".
      For each design decision in a section (defined in "Design decisions"
      below), the architect explains the options, recommends one with the
      reason, and waits for the user to choose. Then the architect writes
      the section, adds each decision the user chose to "Technical
      decisions", shows the user the diff, and commits once the user says
      ok. The "Data" section comes with its change to
      `design/data-model.md`, and "Interfaces" with its change to `api/`,
      in the same diff. A section with no design decisions is shown and
      approved the same way. This part is done when the user has approved
      every section through "Technical decisions".
   2. **The rest, by the architect.** The architect adds the "Requirement
      coverage" and "Tasks" sections of `plan.md`, `tasks/*.md`, and
      `checklist.md`, following the decisions in `plan.md`. A design
      decision that comes up here goes to
      the user as in part 1 before the architect continues. The checklist is
      how the user will test the slice: steps to do by hand, and the tests
      that prove what cannot be seen by hand. A slice has 4–8 tasks; a plan
      that needs more than 8 means the slice is too big, so the architect
      proposes splitting it and the user decides. Each task changes roughly
      300–400 lines, not counting tests, so that one review can check it
      closely.
3. **Plan review.** The reviewer checks the spec, plan, and task files. The
   architect fixes findings marked `Architect`, up to 5 fix rounds; findings
   still open after that go to the user. A plan gets more rounds than code
   (3) because fixing a document is cheap, and every gap found here is one
   the worker will not have to guess. Findings marked `User` go to the
   user.
4. **Plan pull request.** The architect opens it with one comment per review
   round, and sets the slice to `in progress` in `slices/index.md` in the same
   pull request. The user reads the spec, `plan.md`, `checklist.md`, and the
   contract changes, answers open questions, and merges. The user does not
   read the task files; the reviewer checks them in step 3.
5. **Slice branch.** The architect creates `slice/NNNN-name` from `main`.
6. **Tasks.** Built in dependency order on the slice branch (next section).
   After the first task is merged into the slice branch, the architect opens
   the slice pull request as a draft, so the user can follow and try the
   work as it lands.
7. **Slice review.** When every task is in, the reviewer reviews the whole
   slice once (`git diff main...slice/NNNN-name`), looking for problems that
   no single task shows. Findings are fixed like a task's, up to 3 fix rounds.
8. **Try it.** The user goes through `checklist.md` on the slice branch, with
   the architect guiding, and marks each hand check pass or fail. The
   architect runs the tests and fills in the automatic part. A failed row,
   and anything else the user wants changed, becomes a fix (see "How the user
   asks for changes"); after the fix, the user checks that row again.
9. **Done.** When every row passes, the architect commits the filled-in
   checklist, sets the slice to `done` in `slices/index.md` on the slice
   branch, and marks the pull request ready. The user merges. The
   architect tags the merge commit `slice-NNNN-done`, and the user and the
   architect go through the slice together.

## Design decisions

A design decision is a choice about how to build the slice that the user
makes. A choice of any of these kinds is one:

- How the parts talk to each other: protocols, messages, who connects to whom.
- Security: proofs, secrets, who may do what.
- A library outside Go's standard library that tervi does not use yet,
  including one used only by tests or tools.
- A file kept on the user's computer: what it holds and where it lives.
- Any other choice the user asks to make.

`spec.md` says what must be true, as seen from outside; a design decision is
how the code makes it true. Behavior the user can notice (commands, flags,
messages, what happens when) belongs in `spec.md`. When the plan needs
behavior the spec leaves open, the architect asks the user and updates
`spec.md`, with the user seeing the diff.

A slice has as many design decisions as it needs. When it has more than 10,
the slice is too big: the architect proposes splitting it, and the user
decides.

Each design decision is recorded in `plan.md` under "Technical decisions",
in the form shown in `slices/README.md`. Other sections of `plan.md`, the
contracts, and the task files use it and agree with it. A design decision
that comes up during building (a worker question, a new library) goes to
the user as in "When the user must decide"; the architect then records it in
"Technical decisions" and adds it to the file of every task it affects,
before that task continues.

The architect makes every other choice (names inside the code, the task
split, tests, the code inside a package) and writes it in the task file. The
reviewer checks these against the task file and the code layout rules.

## Workflow: a task

1. **Start** a task when every task it depends on is merged into the slice
   branch. Tasks that do not depend on each other may be built at the same
   time.
2. **Worktree.** The architect runs
   `git worktree add ../tervi-task-NNNN-NN -b task/NNNN-NN-name slice/NNNN-name`.
3. **Build.** The architect saves the worker message to a file and runs
   `scripts/run-worker.sh <worktree> <message file>` as a background command;
   Claude Code notifies the architect when Codex finishes, and the command's
   output is the worker report. The worker writes tests and code in the
   worktree, runs the checks, and reports. The worker leaves committing to the
   architect: Codex's sandbox makes git metadata read-only.
4. **Commit.** The architect commits with the worker's suggested message.
5. **Review.** The architect sends the code review message to the reviewer.
6. **Fix.** If the reviewer reports `changes needed`, the architect sends the
   findings to the worker and repeats steps 3–5. After 3 fix rounds without a
   pass, the task stops and the open findings go to the user as a decision
   (see "When the user must decide").
7. **Merge into the slice branch.** On `pass`, the architect merges the task
   branch into `slice/NNNN-name` with `git merge --no-ff`, so each task stays
   one visible merge, and pushes the slice branch. The slice branch is not
   `main`, so the architect may merge into it. The architect posts the task
   comment on the slice pull request, then removes the worktree:
   `scripts/stop-codex.sh <worktree>` first, then `git worktree remove <worktree>`.
   The script stops the Codex plugin's background process for that folder,
   which would otherwise outlive the folder and break any later worktree at
   the same path.

**Fixes after a task is in.** A change the user asks for, or a slice review
finding, is built like a task: a worktree on branch `fix/NNNN-N` (N counts
fixes in the slice) from the slice branch, the worker's fix, a code review,
and a merge into the slice branch with one comment.

## How the user asks for changes

- **A change** is a comment on the pull request, on the line it concerns, or
  a message in chat while trying the slice. For a comment, the user tells the
  architect, for example "PR #5 has comments"; the architect does not watch
  GitHub on its own. Each change becomes a finding for a fix.
- **A question** ("why is this written this way?") goes in chat. If the answer
  shows something should change, it becomes a comment.
- **Unsure what to change:** the user and the architect discuss it in chat
  first, and only the decision goes on the pull request. The architect may
  post that comment for the user, but only after the user approves its exact
  text, and it ends with "(Decided in discussion with the architect.)".
- **A discussion that changes behavior** updates `spec.md` first, with the
  user seeing the diff. One that defers work adds a row to `slices/backlog.md`.
- **Commits the user pushes** go through the reviewer like any other change;
  the user tells the architect.

## When the user must decide

- A worker question, or a reviewer finding marked `User`, stops that task and
  every task that depends on it. Independent tasks continue.
- The architect asks in chat, and posts the same question, options, and
  suggestion as a comment on the slice pull request so it is on record.
- After the user answers, the architect writes the decision down: into
  `spec.md` for behavior, into `plan.md` for a design decision, and into the
  task file for any other choice about how to build it. The user sees the
  diff before the commit.
- Work the user defers goes into `slices/backlog.md`, added in the current
  pull request.

## Rules for every role

- `main` changes only through pull requests that the user merges. Agents
  never merge into `main`; only the architect merges, and only task and fix
  branches into a slice branch.
- Change only the files your task allows.
- When anything is unclear, stop and ask. A guess silently changes the spec.
- Keep secrets out of logs, error messages, test output, and commit messages.
- Commit messages say what changed. Reasons go in reports.
