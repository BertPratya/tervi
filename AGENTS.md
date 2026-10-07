# AGENTS.md

tervi lets a user run coding agents on their own computers from a browser. It
has a Go server (`cmd/server`), a Go worker command (`cmd/tervi`), and
PostgreSQL.

## Commands

```bash
docker compose up -d    # local PostgreSQL
go vet ./...
go test ./...
```

Inside the Codex sandbox the default Go build cache is read-only. Prefix Go
commands with `GOCACHE=/tmp/<worktree folder name>-gocache`.

The sandbox blocks the network unless `.codex/config.toml` allows it. That file
allows it, so tests can reach the local PostgreSQL and Go can download modules.
Start PostgreSQL from outside the sandbox: the architect runs
`docker compose up -d` before starting a worker.

## Roles

| Role | Who | Job |
| --- | --- | --- |
| User | The human owner | Writes specs, makes decisions, reviews and merges every pull request |
| Architect | The main Claude Code session | Plans, dispatches work, commits, opens pull requests, records every review. The only role that talks to the others. |
| Worker | Codex (`gpt-6-luna`, effort `xhigh`) through the Codex plugin, launched by `scripts/run-worker.sh` | Builds one task inside its worktree |
| Reviewer | `.claude/agents/reviewer.md` (`claude-opus-5-5`, effort `high`) | Reviews a plan or a task. Read-only. |

The worker and the reviewer see files and diffs only, never each other's words.

Formats for every file, message, report, and pull request are in
[`slices/README.md`](slices/README.md).

## Workflow: a slice

1. **Spec.** The user writes `slices/NNNN-name/spec.md` while the architect asks
   questions. The user approves it, and it is merged through a pull request.
2. **Plan.** The architect writes `plan.md` and `tasks/*.md` on branch
   `plan/NNNN-name`. A slice has 4–8 tasks; a plan that needs more than 8
   means the slice is too big, so the architect proposes splitting it. Each
   task's pull request must be reviewable in 20–40 minutes: roughly 300–400
   changed lines, not counting tests.
3. **Plan review.** The reviewer checks the spec, plan, and task files. The
   architect fixes findings marked `Architect`, up to 2 fix rounds. Findings
   marked `User` go to the user.
4. **Plan pull request.** The architect opens it with one comment per review
   round, and sets the slice to `in progress` in `slices/index.md` in the same
   pull request. The user answers open questions and merges.
5. **Tasks.** Built one by one, in dependency order (next section).
6. **Done.** When every task is merged, the architect and the user run the
   slice end to end. The architect sets the slice to `done` and tags the commit
   `slice-NNNN-done`.

## Workflow: a task

1. **Start** a task when every task it depends on is merged. To keep working
   while the user is away, build on the unmerged branch of the task below it
   instead (a stack). A stack holds at most one day of work, 3–4 pull requests.
2. **Worktree.** The architect runs
   `git worktree add ../tervi-task-NNNN-NN -b task/NNNN-NN-name <base>`.
3. **Build.** The architect saves the worker message to a file and runs
   `scripts/run-worker.sh <worktree> <message file>` as a background command;
   Claude Code notifies the architect when Codex finishes, and the command's
   output is the worker report. The worker writes tests and code in the
   worktree, runs the checks, and reports. The worker leaves committing to the
   architect: Codex's sandbox makes git metadata read-only.
4. **Commit.** The architect commits with the worker's suggested message.
5. **Review.** The architect sends the code review message to the reviewer.
6. **Fix.** If the reviewer reports `changes needed`, the architect sends the
   findings to the worker and repeats steps 3–5. After 2 fix rounds without a
   pass, the architect opens the pull request marked `Needs you`.
7. **Pull request.** On `pass`, the architect pushes the branch, opens the
   pull request with one comment per review round, and removes the worktree:
   `scripts/stop-codex.sh <worktree>` first, then `git worktree remove <worktree>`.
   The script stops the Codex plugin's background process for that folder,
   which would otherwise outlive the folder and break any later worktree at
   the same path.
8. **User review.** The user reviews. Change requests go to the worker as
   findings, followed by a new review round and a new comment. See
   "How the user asks for changes" below.
9. **After merge.** The architect updates local `main`, rebases any stacked
   branches onto it, and starts the next task.

## How the user asks for changes

- **A change** is a comment on the pull request, on the line it concerns. The
  user then tells the architect, for example "PR #5 has comments"; the
  architect does not watch GitHub on its own. Each comment becomes a finding
  for the worker.
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
- The architect opens a draft pull request titled `… (needs your decision)`
  with the question, the options, and a suggestion.
- After the user answers, the architect writes the decision down: into
  `spec.md` for behavior, into the task file for how to build it. The user
  sees the diff before the commit.
- Work the user defers goes into `slices/backlog.md`, added in the current
  pull request.

## Rules for every role

- `main` changes only through pull requests that the user merges. Agents never merge.
- Change only the files your task allows.
- When anything is unclear, stop and ask. A guess silently changes the spec.
- Keep secrets out of logs, error messages, test output, and commit messages.
- Commit messages say what changed. Reasons go in reports.
