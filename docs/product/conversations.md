# 5. Chat and Agent Interaction

## 5.1. Purpose

Chat is one way to work inside a workspace. Users can ask an agent to inspect files, change code, research a topic, perform calculations, or produce results while keeping a saved conversation about that work.

The platform should support different agent harnesses without tying the whole application to one provider. Users should also be able to change harnesses within a conversation, with a clear explanation of how context is passed to the new agent.

This section records the interaction design. Detailed execution lifecycles, permission policies, storage schemas, and concurrency rules will be designed separately.

## 5.2. Multiple chats in one workspace

A workspace can contain several chats. Each chat has its own history and agent session context, while belonging to the same workspace.

For example, a workspace called **Study App** might contain:

| Chat | Purpose |
| --- | --- |
| Build the notes page | Implement a feature. |
| Investigate a failing test | Find and fix an error. |
| Explain the project structure | Learn how the code works. |

Creating another chat does not create another workspace or automatically copy its files. Separate chats do not automatically know each other's conversations.

Multiple chats also do not imply that simultaneous file edits are safe. Rules for concurrent agents, shared files, and isolated working copies remain a separate decision.

## 5.3. Platform conversation and harness sessions

The platform conversation is the persistent chat the user sees. A harness session is the context maintained by a particular agent implementation.

| Concept | Meaning |
| --- | --- |
| Workspace | The project containing files, chats, settings, and work history. |
| Platform conversation | A saved chat that remains stable even when the user changes harnesses. |
| Harness session | The native session or thread used by Codex, Claude Code, or another supported harness. |
| Turn | A user request and the agent work performed in response. |

A conversation can become linked to several harness sessions over time. Switching from Codex to Claude Code does not mean that Claude resumes Codex's native thread. The platform starts a session for Claude and supplies a context handoff.

Earlier messages retain their original agent and model labels. A switch must not make it appear that the new agent produced the previous agent's work.

## 5.4. Harness and model selection

The chat input provides an agent selector. Users can choose Codex, Claude Code, or another installed and supported harness.

Harness selection and model selection are separate concepts:

- **Harness:** the software that manages the agent's tools, execution, and session behavior.
- **Model:** the model offered through that harness and its configured provider.

The model choices should come from the selected adapter's supported configuration. The interface must not imply that every model can be used with every harness.

Show whether the selected harness is available and authenticated in the workspace's execution environment. If it is unavailable or needs login, explain what the user must do before sending work.

Record which harness and model actually handled each turn. Changing a selection must not rewrite past history.

## 5.5. Supporting additional harnesses

Open-source contributors should be able to add a harness through a defined adapter interface without rewriting workspace management or chat rendering.

The adapter connects the harness's native protocol to the platform's common operations and events. Shared responsibilities include starting sessions, sending messages, receiving progress, handling approvals, and reporting completion or failure.

Adapters must declare their capabilities. Candidate capability fields include:

| Capability | What the platform needs to know |
| --- | --- |
| Streaming | Can the harness provide incremental responses or activity? |
| Steering | Can a new message guide an already active task? |
| Cancellation | Can the platform request that active agent work stop? |
| Approvals | Can permission requests and user decisions be exchanged? |
| Session resumption | Can a previous native session be resumed? |
| Model selection | Which model choices or changes are supported? |
| Context input | How can the adapter supply a handoff or selected history? |

The interface should adapt to these declarations. For example, a harness without steering support should keep new messages queued instead of displaying a steering control that does nothing.

### Preserve each harness's capabilities

The design goal is to expose every capability of each integrated harness through the browser. Provide a consistent common interface while preserving access to harness-specific features, and make integration gaps explicit. The common adapter contract must not reduce all harnesses to the features they share.

| Layer | Responsibility |
| --- | --- |
| Shared capabilities | Provide consistent controls for common operations where supported, including messages, streaming, approvals, stopping, and session management. |
| Harness-specific capabilities | Expose additional actions, settings, events, and results offered by the selected harness, even when no other harness offers them. |
| Capability discovery | Declare what the adapter supports and what is available in the selected harness version and execution environment, so the browser can present the appropriate controls. |

For example, expose steering when the selected harness supports it. A different harness may have a unique mode or setting; it should receive its own browser control rather than being dropped because it has no equivalent elsewhere. Shared controls must preserve the harness's actual behavior rather than implying identical semantics across providers.

### Browser controls and capability status

Keep frequent controls near the chat workflow. Place advanced or harness-specific settings in expandable areas so broad feature coverage does not overwhelm normal use. Adapters need an extension mechanism for features that do not fit the common interface; the exact schema and rendering mechanism remain implementation choices.

Distinguish these states in capability details:

| State | Meaning and presentation |
| --- | --- |
| Supported | The browser integration implements the feature and it is available in the current context. |
| Not yet integrated | The harness offers the feature, but the adapter or browser implementation does not expose it yet. Identify the gap rather than implying the harness lacks the feature. |
| Unavailable | The selected harness or execution environment cannot provide the feature in the current context. Explain the reason where known, including any prerequisites that could enable it. |

Do not show nonfunctional controls as available. Keep unsupported features out of the main action area while allowing users to inspect capability coverage and limitations. If the platform cannot determine a capability's status, report that uncertainty rather than inventing support.

### Coverage goal and compatibility boundaries

Full capability coverage is the intended goal, not an unconditional promise of immediate parity with every harness release. Some features may lack a supported programmatic interface or require additional browser implementation. Record those gaps and track them as adapter work; do not silently discard them.

Use supported integration interfaces and preserve applicable permissions and approval behavior. Where a capability cannot be exposed through those interfaces, explain the limitation instead of bypassing harness boundaries. Capability coverage should be checked against the integrated harness version and revisited when that version changes.

The feature inventory should cover interactions and settings as well as meaningful output events. Compact presentation in chat may hide detail behind expansion, but must not make a supported feature's results or required user actions inaccessible. This complements the event-display rules in section [5.7](conversations.md#57-progress-results-and-event-filtering).


The exact extension packaging, adapter API, and compatibility tests remain implementation work. The intended contributor experience is a documented adapter contract with an example integration.

## 5.6. Sending messages while the agent is busy

New messages enter a queue by default when the agent is already working. Sending a message should not silently interrupt the current task.

The interface must distinguish a queued message from a message already delivered to the agent.

| User action | Expected behavior |
| --- | --- |
| Send while the agent is idle | Begin the next turn. |
| Send while the agent is busy | Queue the message for later processing. |
| Explicitly choose to steer | Deliver the message to the current task if the harness supports it. |
| Try to steer an unsupported harness | Explain the limitation and retain the message in the queue. |

Steering means providing direction during the current task. It does not guarantee an immediate interruption of a tool that is already running.

For example, while the agent is building a page, the user sends "Use a dark background." By default, that message waits in the queue. If the user chooses to steer, the platform sends it as guidance for the active task where supported.

A successfully delivered steering message must not later be submitted again as a separate queued task. Delivery failures should remain visible rather than silently losing the user's message.

Queue editing, reordering, and the exact dispatch policy after a failure or cancellation remain to be designed.

## 5.7. Progress, results, and event filtering

The chat should show useful activity while keeping the main conversation readable. Users should not need to inspect raw protocol messages to understand the agent's progress.

| Display category | Examples | Presentation |
| --- | --- | --- |
| Essential | Final responses, approval requests, errors, and completion status. | Always accessible in the conversation; important requests and failures clearly surfaced. |
| Work details | Tool activity, commands, file changes, and progress updates. | Compact summaries with expandable details. |
| Internal traffic | Heartbeats, protocol bookkeeping, and duplicate updates. | Hidden from normal chat. |

Show the final result and references to relevant changed files or generated outputs. Detailed artifact viewing and version history will be defined separately.

Progress should describe what is actually known. Do not invent a percentage or estimated completion time when the workload does not report one.

**Display filtering and event retention are different rules.** Hiding an event from chat does not automatically mean discarding it. Preserve the information needed to restore history, explain failures, and track important actions under the platform's retention policy. This does not require saving every heartbeat or every streamed text fragment forever.

## 5.8. Switching harnesses within the same chat

Users can switch harnesses while keeping the same platform conversation and visible history.

The existing platform conversation remains the same when the user switches harnesses. On first use of the destination harness, start its native session and supply a context handoff, which may summarize the earlier conversation. Returning to a previously used harness follows section [5.11](conversations.md#511-returning-to-a-previous-harness). A switch does not transfer the previous harness's complete internal state.

Before switching, warn that the handoff may omit context and recommend creating a new chat/session with the destination harness. The user may still choose to continue in the existing conversation.

### If the agent is working

Offer two choices:

1. **Wait until finished:** complete the current turn before switching.
2. **Stop current task and switch:** request cancellation, confirm that the current agent work has stopped, then switch.

If stopping fails or the work computer is disconnected, show that the task state is uncertain. Do not claim it stopped or silently let a replacement agent take over the same task.

Stopping the active task does not delete the previous session or undo changes already made. Independently managed background jobs need their own lifecycle rules; a harness switch must not silently terminate unrelated work.

### Switch flow

1. The user selects the destination harness.
2. Resolve active work by waiting or stopping it.
3. Warn about context-transfer limitations and recommend a new chat/session; let the user choose whether to continue in the existing conversation.
4. For continuation, prepare a handoff from the saved conversation and latest known work state. Explain what context will be passed before sending it to the destination provider.
5. Start or resume the destination harness session as described in sections 5.8 and [5.11](conversations.md#511-returning-to-a-previous-harness), in the same workspace, and deliver the handoff.
6. Add a visible switch marker to the chat and continue the conversation.

If the new session cannot start, retain the conversation and previous session history and show the error. Do not present the switch as successful.

Queued messages must remain accounted for during the transition. The exact rule for assigning them to the old or new harness is still open and must be made clear before dispatch.

## 5.9. Context handoff

The handoff should preserve the information needed to continue the user's work:

- The current task and user constraints.
- Important decisions and their reasons.
- Work already completed and known results.
- Relevant file locations and changes.
- Remaining work, unresolved questions, and known problems.
- The latest user instructions and useful recent messages.

Use a summary and selected conversation history as the baseline. Include more history when the receiving adapter supports it and there is sufficient context space. Passing the visible transcript still does not guarantee identical agent behavior or complete native-session continuity.

Do not assume internal reasoning, live processes, tool state, or approval grants can be transferred between harnesses. The destination session must use its own supported tools and the platform's applicable permission rules.

A summary can omit or misstate details. Preserve the original history so users can inspect it, and allow relevant files or earlier messages to be consulted through supported mechanisms. Treat descriptions of file state as a starting point to verify, not proof that files have remained unchanged.

The exact summarization mechanism remains open. If a model generates the summary, use an authenticated, supported integration and account for its usage and latency. Do not make handoff generation depend solely on an agent that has already failed or become unavailable.

## 5.10. Switching notice and fresh-start option

Before transferring context, show a plain-language notice such as:

> Switching agents may use a summary and selected conversation history to pass context. Some details may be omitted, and the new agent cannot inherit the previous agent's complete internal state. We recommend creating a new chat/session with the selected agent. You can still continue in this chat; transferred context will be sent to the selected provider. Your existing chat history stays available.

Offer these choices:

| Choice | Effect |
| --- | --- |
| Continue in this chat | Start or resume the destination harness session with the handoff while preserving the visible conversation. |
| Create a new chat/session — recommended | Create a separate conversation with the destination harness without automatically importing the earlier conversation. |

Show the warning and new-chat recommendation whenever the user switches harnesses. A new chat is recommended, not required; switching within the existing conversation remains supported.

Clearly identify the context boundary when starting a new chat/session. The new conversation still has access to the selected workspace files according to its permissions; it does not create an empty project.

## 5.11. Returning to a previous harness

Keep previous harness-session references and saved conversation history after switching.

If the user later returns to an earlier harness, it must receive the relevant work and decisions made since it was last active. Resuming its old session without that update could cause it to act on outdated assumptions.

Where supported, resume the earlier native session and supply the missing context. Otherwise, start another session with a fresh handoff. The precise resume-versus-new-session policy will be decided with the adapter and execution design.

## 5.12. Example

Bert opens the **Build the notes page** chat and selects Codex. Codex implements the page and reports the files it changed.

Bert then chooses Claude Code to review the result. The platform warns that switching may lose context and recommends a new chat/session. Bert chooses to continue in the existing chat, so the platform prepares a handoff describing the requirements and completed changes and explains that Claude will receive summarized context.

After Bert chooses **Continue in this chat**, the platform starts Claude's session in the same workspace. A chat marker shows the switch. Claude can inspect the current files and continue the review.

If Codex had still been working, Bert would first choose to wait or stop that task. Previous messages and code changes would remain available either way.

## 5.13. Scope and remaining decisions

The initial use is personal, with no account sharing. Authentication remains provider-specific. Context handoff is a transfer of task information, not a transfer of provider credentials. Detailed provider compliance belongs in the authentication and integration requirements and must be checked against the actual implementation.

**Decisions recorded:** multiple chats per workspace, selectable harnesses and models, extensible adapters, capability-aware controls, queued messages with explicit steering, filtered activity display, and harness switching within an existing chat through an explained context handoff while preserving platform history. Each switch warns about context loss and recommends a new chat/session without requiring it.

**Still open:** concurrent file editing, queue behavior during switches and failures, exact adapter packaging, handoff generation and size limits, and native-session reuse rules. Permissions, managed execution, artifacts, and data schemas will be covered separately.
