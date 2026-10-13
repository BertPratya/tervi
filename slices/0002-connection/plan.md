# 0002 Connection — plan

Status: draft
Spec: [spec.md](spec.md)

## Overview

| Part | Location | Notes |
| --- | --- | --- |
| Connection on the server | `internal/server/connection` | Accepts each worker's connection, checks its credential, keeps one connection per machine (newest wins), runs the checks that notice a lost worker, knows which machines are Online, and records "last seen"; `Register(mux, ...)` for its route |
| Machines list | `internal/server/machines` | The list the Machines page shows: every paired machine with its name, OS, Online or Offline, and "last seen"; `Register(mux, ...)` for its route |
| Machines page | `internal/server/web` | `/machines`, plain HTML and JavaScript next to the approval page, refreshing itself every 5 seconds |
| Database | `internal/server/db` | A migration that adds "last seen" to machines |
| Connection messages | `internal/protocol` | Shared by both sides: the messages sent over the connection, and the check timing the server sends the worker |
| Daemon | `internal/worker/daemon` | The long-running program: obeys `connect`, connects, reconnects with growing waits, stops on a rejected credential, allows one daemon per user account, writes the log lines |
| Talking to the daemon | `internal/worker/control` | How `tervi status` and `tervi connect` reach the running daemon, and its answers |
| Background service | `internal/worker/service` | Sets up, starts, and enables the systemd user service; checks that tervi is installed (R28); reads the log for `tervi logs` |
| Worker command line | `internal/worker/cli` | Adds `tervi connect`, `tervi status`, `tervi logs`, and `tervi daemon`; after pairing, starts the background service and waits for the connection |
| Worker local record | `internal/worker/state` | Adds `connect` and the rejection reason to `state.json` |
| End-to-end test | `test/e2e` | Adds the connection: pairing connects, the server stops and comes back, a rejected credential |
| API contract | `api` | `openapi.yaml` gets the connection route and the machines list; a new `asyncapi.yaml` holds the messages sent over the connection |

The new server packages register their own routes, as in slice 1, so tasks
that add routes do not edit the same file.
