# Slices

A slice is one thin piece of a feature that works end to end and can be
demonstrated. Each slice lives in its own folder. [index.md](index.md) lists
every slice and its status.

## Folder layout

```text
slices/
  index.md                 # one row per slice
  NNNN-name/
    spec.md                # what to build. Written by the user.
    plan.md                # how to build it, plus the pull request list. Drafted by AI, approved by the user.
    tasks/
      NN-name.md           # one file per pull request. Drafted by AI, approved by the user.
```

`NNNN` is a four-digit number in creation order, for example `0001-pairing`.

## Who reads what

| Role | Reads | Writes |
| --- | --- | --- |
| User | Everything | `spec.md`; approves everything else |
| Architect | `spec.md`, `docs/product/`, the code | `plan.md`, `tasks/*.md` |
| Worker | Its one task file, the code, `AGENTS.md` | Code, tests, the pull request |
| Reviewer | The task file, the spec requirements, the diff | A verdict on the pull request |

The worker never reads `spec.md` or `plan.md`. Each task file copies in the
requirements and context it needs.

## spec.md format

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

## Status values

Used in `index.md`.

| Status | Meaning |
| --- | --- |
| draft | The spec is being written |
| approved | The spec is approved; planning or building can start |
| in progress | Pull requests are being built and merged |
| done | Every requirement is met and the slice was demonstrated end to end |
