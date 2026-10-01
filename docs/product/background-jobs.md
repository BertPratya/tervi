# 6. Background Tasks and Long-Running Work

## 6.1 Purpose and scope

The platform supports work that continues beyond a single agent response, such as training a model, running a simulation, processing a dataset, or starting a development server. Users can launch work, inspect its progress, leave the interface, and return to its results.

This section records the agreed product behavior. Detailed delivery acknowledgements, event storage, deduplication, and session recovery belong in a separate **[Connection Recovery and Event Synchronization](recovery.md)** section.

## 6.2 Agent work and background jobs

| Type | Meaning | Example |
| --- | --- | --- |
| Agent work | The harness is processing a task, using tools, or waiting for approval. | Investigate a bug and implement a fix. |
| Background job | A separately tracked process that can continue independently of the agent's current turn. | Run a two-hour training script. |

Each tracked job has its own identity and links to its workspace and originating chat. Where applicable, it also links to the agent turn that launched it.

Ordinary file reads and short commands can remain expandable chat activity. Commands explicitly launched as tracked jobs receive independent status, logs, and controls. An arbitrary command launched inside a harness is not automatically a platform-managed job.

## 6.3 Launching and execution

1. The user or agent requests a tracked job in the selected workspace.
2. The execution service launches the process in the appropriate directory and records its identity.
3. The service captures output and tracks process status.
4. It sends logs and status updates to the main server, which saves and forwards them to the browser.
5. Once the launch has been confirmed, the agent can finish its turn and become available for further conversation while the job continues.

For a connected computer, the execution service is the connector on computer B. For a managed workspace, it is the workspace service in that execution environment. The main server on computer A coordinates access and history; it does not execute remote jobs itself.

Launching a job must be integrated with the harness through the platform's job interface. The exact adapter or tool mechanism remains an implementation decision.

The existing queue and steering rules from section [#5](conversations.md) still apply while an agent turn is active. Background execution does not imply that the same agent session can process unlimited simultaneous turns.

## 6.4 Progress and controls

Show a compact job card in the originating chat. Jobs are also accessible through an optional **Jobs** panel, following the panel and layout rules in section [#4](interface.md).

| Information or action | Behavior |
| --- | --- |
| Job name and location | Identify the work, workspace, and execution computer. |
| Status | Distinguish queued, running, completed, failed, and cancelled work; show waiting for input or approval when applicable. |
| Timing | Show elapsed time and the last received update. |
| Progress | Show percentages, epochs, or steps only when the program reports them reliably. |
| Logs | Provide live normal and error output, with access to retained logs later. |
| Outputs | Link to available result files in the workspace. |
| Stop | Request cancellation of that specific job. |
| Origin | Allow navigation back to the chat that launched the job. |

Example:

> **Training model — Running on Research Desktop**  
> Elapsed: 18 minutes · Epoch 12/50  
> View logs · Open outputs · Stop

Long logs should remain expandable rather than flooding the chat. Display filtering must not silently discard information needed for history or recovery.

## 6.5 Stopping agents and jobs

- Stopping an agent's current task leaves independently managed background jobs running.
- Each background job has its own Stop control.
- Closing the chat, switching chats or harnesses, disabling the IDE, or closing the browser does not itself stop a managed background job.
- Stopping a job does not delete its history or already-produced files.
- A stop request is not proof that a process has stopped. Show the request as pending until the execution service confirms the outcome.

When the execution computer is unreachable, the interface must explain that it cannot immediately confirm or enforce a stop request.

## 6.6 Disconnection behavior

### Browser disconnection

Closing the browser or losing the browser-to-server connection leaves existing work running while the execution environment remains available. On return, users can view saved activity and the latest known status.

### Execution computer disconnection

If computer B loses its connection to server A:

- Existing agents and jobs continue where their local execution and provider access allow it.
- A displays **Disconnected — last known status: running**, or the corresponding last known state, with the time of the last update.
- Disconnection is a connectivity state; it does not prove that a job failed, stopped, or is still running.
- B retains pending events and logs locally for later delivery.
- Actions requiring an approval from the remote user wait for that approval.
- New messages submitted through the browser can remain queued on A until B reconnects.
- On reconnect, the platform delivers missing activity and reconciles actual agent and job status without launching the same work twice.

A network outage differs from the execution computer sleeping, shutting down, or crashing. The first version does not promise that a process survives a machine restart or resumes its computation automatically.

### Recovery boundary

The connector's durable event log supports normal synchronization; native harness session storage supports agent-session recovery where the harness permits it. Scanning and comparing all local session files is not the normal reconnect mechanism.

The separate synchronization section will define event identifiers, acknowledgements after durable server storage, replay of unacknowledged events, duplicate handling, and recovery when the connector misses events or crashes.

## 6.7 Completion and history

When a job finishes, fails, or needs attention, notify the user in the platform and update its chat card and Jobs entry. If completion occurs while disconnected, deliver the update after reconnection.

Keep the outcome, timestamps, logs, and links to available outputs accessible from the originating chat after completion. A saved link does not guarantee that a user has not subsequently moved or deleted the underlying file; show unavailable outputs honestly.

Automatic agent analysis of completed results is optional. Completion alone should not silently start a new agent task. The exact opt-in setting and follow-up behavior remain to be designed.

## 6.8 First implementation phase

The first version includes:

- Explicit launch and tracking of background jobs.
- Independent job execution while the agent becomes available again.
- Job cards and an optional Jobs panel.
- Status, timing, live logs, output links, and cancellation.
- Completion, failure, and attention notifications.
- Saved job history and links to the originating conversation.
- Continued execution during browser disconnection and honest status during execution-computer disconnection.
- Delivery of pending activity and status reconciliation after reconnection.

The design remains single-user-first and does not require a team task-management system.

## 6.9 Planned future phases

These features remain in the intended roadmap; postponement does not remove them from scope.

| Future feature | Intended behavior | Details to decide later |
| --- | --- | --- |
| Scheduling | Launch work at a chosen time or on a recurring schedule. | Time zones, missed schedules, offline machines, and overlapping runs. |
| Workflows and workflow builder | Connect dependent jobs, such as process data → train → evaluate. | How outputs pass between steps, failure behavior, and workflow editing. |
| Automatic retries | Retry eligible failures with configurable limits and delays. | Which failures qualify and how to avoid repeating harmful or non-repeatable effects. |

Automatic retries start another attempt; they do not inherently resume a program from its last computational checkpoint. Checkpoint support depends on the program and requires a separate design if needed.

## 6.10 Related design boundaries

- **Section [#4](interface.md):** panel layout, optional IDE loading, presets, and layout locking.
- **Section [#5](conversations.md):** conversations, harness adapters, queues, steering, event display, and context handoffs.
- **Future synchronization section:** reliable event delivery, command deduplication, local storage, and session recovery.
- **Git and change review:** reuse Theia's Git capabilities where appropriate; task-specific attribution and any review experience outside the IDE remain separate decisions. The frontend is built around Theia; see section [4.8](interface.md#48-theia-application-foundation-and-customization).

## 6.11 Reference informing the design

Vibe Kanban provides a useful product reference for process tabs, live stdout/stderr logs, development-server controls, and workspace status. These demonstrate monitoring behavior; they do not establish that arbitrary computations survive machine failures.

- [Vibe Kanban interface guide](https://vibekanban.com/docs/workspaces/interface)

The behavior specified above is this platform's agreed design, not a claim that Vibe Kanban implements every requirement.
