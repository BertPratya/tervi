# Roadmap

The order slices are expected to come in, and what each lets the user do. It
is a direction, not a promise: each slice still gets its own spec, and trying
a slice can change the ones after it. The product design behind it is in
[`docs/product/`](../docs/product/). The status of each slice is in
[index.md](index.md).

## The picture

The user signs into Codex on their own computer and pairs it with tervi. From
a browser, they pick that computer, pick a folder on it as a workspace, and
chat with Codex there. Codex works on that computer, in that folder; its
replies, actions, and approval requests stream to the browser, and the server
saves them, so the user can close the browser and come back, or follow from
another device. Files never leave the computer.

Coding is the first thing tervi does, not the only one. tervi is the
foundation for the user's own AI platform: computers, workspaces, chats with
an agent, and extensions on top. A personal assistant, and later extensions
such as a study tool or stock analysis, are built on the same foundation.
The slices below build the foundation first.

## Slices

| Slice | After it, the user can | Notes |
| --- | --- | --- |
| 0001 | Pair a computer with the server | Done |
| 0002 | See each computer Online or Offline; tervi runs in the background and reconnects by itself | In progress |
| 0003 | Disconnect (`tervi disconnect`) and unpair a computer | Decided during 0002. See backlog B12 |
| 0004 | Register a folder on a computer as a workspace | The first request the server sends down the connection and waits for an answer to |
| 0005 | Chat with Codex in a workspace: send a message, watch the reply stream in, see when it is done | `codex app-server`; raises the 4 KB message limit or splits large output |
| 0006 | Answer Codex's approval requests in the browser | |
| 0007 | Close the browser and come back to the full history; nothing lost or doubled when the computer disconnects mid-task | Event IDs, acknowledgements, replay (`docs/product/recovery.md`) |
| 0008 | Stop a running task; queue messages while the agent is busy | |

## Later, order not decided

- Sign-in and HTTPS, so tervi can be used from another device outside the
  computer itself. Today the server listens on `localhost` only. If remote
  use is wanted early, this moves before 0005.
- Claude Code as a second agent, and switching agents within a chat.
- Several chats per workspace.
- The Theia IDE panel next to the chat.
- Background jobs.
- Managed workspaces (storage and execution provided by the platform).
- Extensions: features added on top of the foundation, installed and turned
  on from the browser, like extensions in VS Code. Each runs as its own
  program and talks to tervi through a defined interface, so it can be
  written in another language, such as Python. Examples: search over the
  user's own files (RAG), a study tool, stock analysis.
- Personal assistant: chats about anything, not only code, with memory that
  lasts across chats, tasks that run on a schedule, and a way to reach the
  user when they are away. It starts with an agent the user is already
  signed into (Codex).
- The user's own agent, added the same way as Codex and Claude Code. How it
  pays for model use is a setting: an API key, a model on the user's own
  computer, or the user's ChatGPT plan where OpenAI allows it.
