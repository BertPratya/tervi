#!/usr/bin/env bash
# Stops the Codex plugin's background processes for one worktree.
# Run it before removing the worktree; otherwise a later worktree at the same
# path reuses a process whose folder no longer exists.
#
# Usage: scripts/stop-codex.sh <worktree path>
set -euo pipefail

worktree="$(realpath -m "${1:?usage: scripts/stop-codex.sh <worktree path>}")"

# The plugin's broker: a node process started with --cwd <worktree>.
ps -eo pid=,args= | awk -v cwd="--cwd $worktree " '
  $2 ~ /node$/ && /app-server-broker\.mjs serve/ && index($0 " ", cwd) { print $1 }
' | xargs -r kill

# The Codex app-server running in that folder.
for pid in $(pgrep -x codex || true); do
  dir="$(readlink "/proc/$pid/cwd" 2>/dev/null || true)"
  if [ "${dir% (deleted)}" = "$worktree" ]; then
    kill "$pid"
  fi
done
