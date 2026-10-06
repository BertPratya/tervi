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

## Roles

| Role | Who | Job |
| --- | --- | --- |
| User | The human owner | Writes specs, makes decisions, reviews and merges every pull request |
| Architect | The main Claude Code session | Plans, dispatches work, commits, opens pull requests, records every review. The only role that talks to the others. |
| Worker | `.claude/agents/worker.md`, which runs Codex (`gpt-6-luna`, effort `xhigh`) | Builds one task inside its worktree |
| Reviewer | `.claude/agents/reviewer.md` (`claude-opus-5-5`, effort `high`) | Reviews a plan or a task. Read-only. |

The worker and the reviewer see files and diffs only, never each other's words.

Formats for every file, message, report, and pull request are in
[`slices/README.md`](slices/README.md).

## Workflow: a slice

1. **Spec.** The user writes `slices/NNNN-name/spec.md` while the architect asks
   questions. The user approves it, and it is merged through a pull request.
2. **Plan.** The architect writes `plan.md` and `tasks/*.md` on branch
   `plan/NNNN-name`.
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
3. **Build.** The architect sends the worker message. The worker writes tests
   and code in the worktree, runs the checks, and reports. The worker leaves
   committing to the architect: Codex's sandbox makes git metadata read-only.
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
   findings, followed by a new review round and a new comment.
9. **After merge.** The architect updates local `main`, rebases any stacked
   branches onto it, and starts the next task.

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
