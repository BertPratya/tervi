#!/usr/bin/env bash
# Runs one worker message in Codex through the Codex plugin, and prints
# Codex's answer (the worker report). The architect runs this as a background
# command and is notified when it finishes.
#
# Usage: scripts/run-worker.sh <worktree> <message file>
set -euo pipefail

model="gpt-6-luna"
effort="xhigh"

worktree="$(realpath "${1:?usage: scripts/run-worker.sh <worktree> <message file>}")"
message="$(realpath "${2:?usage: scripts/run-worker.sh <worktree> <message file>}")"

# The plugin's own runtime script, newest installed version.
companion="$(ls -d ~/.claude/plugins/cache/openai-codex/codex/*/ | sort -V | tail -1)scripts/codex-companion.mjs"

output="$(mktemp)"
trap 'rm -f "$output"' EXIT

# A model at capacity fails at once; wait and start again, 3 starts at most.
for attempt in 1 2 3; do
  node "$companion" task --fresh --write --model "$model" --effort "$effort" \
    --cwd "$worktree" --prompt-file "$message" >"$output" 2>&1 || true
  if ! grep -q "at capacity" "$output"; then
    break
  fi
  if [ "$attempt" -lt 3 ]; then
    sleep 60
  fi
done

cat "$output"
