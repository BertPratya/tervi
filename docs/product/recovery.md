# 11. Connection Recovery and Synchronization

## 11.1. Independent execution

After the execution computer has durably accepted a task, it manages that task locally. Agent execution and independently tracked jobs must not depend on the coordinator connection remaining open. The connector's execution supervision and network delivery responsibilities have separate lifecycles.

A network outage interrupts delivery of progress and results, new instructions, approvals, and stop requests. Existing work continues where local execution and provider connectivity permit it. An action requiring a new remote decision waits; disconnection never implies approval.

The coordinator shows **Disconnected** with the last known activity and update time. It must not infer completion, failure, or cancellation solely from connection loss. These rules apply to connected computers and equivalent services in managed execution environments.

## 11.2. Task dispatch and receipt acknowledgements

The coordinator durably saves each task with a stable request ID before dispatch. The worker validates the request and persists its acceptance in a local task record before acknowledging receipt. A rejected request is reported explicitly and is not presented as accepted.

| Participant | Responsibility |
| --- | --- |
| Coordinator | Save the task and retry delivery with the same request ID until worker receipt is confirmed. |
| Worker | Persist accepted requests, recognize repeated IDs, and return existing receipt/status without launching another copy. |

Once the coordinator receives and records the worker's acknowledgement, it stops dispatch retries. Receiving a task does not mean the task has started or completed; those states are reported separately.

The worker does not need to repeatedly send receipt acknowledgements on its own. It responds to initial dispatch, repeated dispatch, or status reconciliation. No third message acknowledging the receipt acknowledgement is required.

Example of a lost acknowledgement:

1. The coordinator sends task `123`.
2. The worker saves acceptance of `123`, acknowledges it, and schedules local execution.
3. The acknowledgement is lost before reaching the coordinator.
4. The coordinator retries task `123` using the same ID.
5. The worker recognizes `123` and returns its known state without starting duplicate work.

Duplicate checking and task acceptance must be coordinated so simultaneous retries cannot both launch the task. Reusing an ID with different task contents is a conflict, not an instruction to replace the existing task.

Retain task deduplication records separately from outgoing events. Removing acknowledged event copies must not remove the evidence needed to recognize an old task dispatch. Exact record retention and rejection of stale requests remain implementation details.

## 11.3. Durable worker event queue

Use a persistent local outgoing queue, or durable outbox, for events awaiting delivery to the coordinator. This is a delivery queue, separate from the user-message queue in section [#5](conversations.md) and the accepted-task records above.

1. Record each event locally before sending it, with a stable stream/session identifier and increasing sequence number.
2. A delivery loop sends pending events while connected.
3. The coordinator durably saves each event and then acknowledges it.
4. The worker removes or reclaims the acknowledged event's delivery copy.
5. Unacknowledged events remain available for replay after reconnecting or restarting the delivery process.

Sending data into a socket, or receiving it into server memory, is not sufficient confirmation. Acknowledgement means the coordinator has durably saved the event or has verified that it already holds that same event.

Checking whether the queue is empty is useful for scheduling delivery but does not prevent duplicate history. Stable event identity and server-side duplicate handling provide that protection.

## 11.4. Replay, ordering, and duplicate handling

The coordinator treats a repeated event ID as the same event and acknowledges it without inserting a second history entry. A repeated ID with conflicting content must be reported as a consistency error.

An acknowledgement can identify individual events or a contiguous saved sequence. For example, **saved through 95** permits removing events 1–95 from that stream's delivery queue only if all those events are accounted for. Receiving event 97 alone must not imply that missing event 96 was saved.

If the worker has events 1–120 and the coordinator has acknowledged through 95, reconnecting replays 96–120. If the acknowledgement for 120 was lost, a repeated delivery is harmless to recorded history because the coordinator recognizes the event IDs.

Preserve sequence identity across reconnects and worker restarts. If a new stream must be created, give it a distinct identity rather than reusing old event IDs. Reconcile the coordinator's durable receipt position and the worker's pending queue before advancing cleanup.

These rules provide retryable delivery and duplicate suppression; they do not promise exactly-once effects for arbitrary external commands. Retrying delivery of an existing task is different from intentionally starting a new execution attempt after failure.

## 11.5. What remains stored on the worker

| Local information | Purpose |
| --- | --- |
| Accepted-task records | Track receipt, execution state, and duplicate dispatches. |
| Pending-event queue | Retain events until coordinator storage is acknowledged. |
| Native harness session state | Support the harness's own continuation and recovery mechanisms. |
| Job/process records and retained files | Identify existing work and preserve inputs, outputs, and applicable logs. |

Queue cleanup removes unnecessary delivery copies, not native agent context, working files, or separately retained logs. Those follow their own lifecycle and retention policies.

Normal reconnection uses the event queue and status reconciliation rather than scanning and comparing all native session files. When events were missed or a process crashed, supported history/resume interfaces or documented session storage may assist recovery. Do not assume that a harness persists every streamed event or that missing records can always be reconstructed.

## 11.6. Reconnection and crash recovery

On reconnect, the worker authenticates, replays pending events, and reports actual task/job states and pending approvals. Reconcile live state with saved history so a late event does not silently overwrite a newer confirmed outcome. Detailed state-transition and reconciliation rules will be specified with the data model.

| Interruption | Expected behavior |
| --- | --- |
| Browser disconnects | Existing work continues; the browser reloads saved activity and current known state on return. |
| Worker-to-coordinator connection drops | Continue eligible local work, retain pending events, and reconnect automatically. |
| Coordinator restarts | Recover saved task/event state, reconnect workers, and reconcile unconfirmed dispatches using their original IDs. |
| Connector crashes | Inspect persisted tasks and surviving processes before taking further action; do not assume all children survived or all stopped. |
| Agent crashes | Preserve available history and report the interruption; use supported session recovery where possible. |
| Execution computer shuts down | Processes stop; recover surviving files and records, without promising arbitrary computation resumption. |

A crash can occur between launching a process and recording its identity. If it is unclear whether work started or produced effects, show **Execution state uncertain** and reconcile before restarting. A durable receipt record alone is not proof that execution occurred exactly once.

Do not automatically repeat potentially consequential work just because its completion event is missing. Resume supported sessions or request an explicit restart decision when uncertainty cannot be resolved. General automatic retries remain the future feature described in section [#6](background-jobs.md).

Approval reconciliation follows section [#10](permissions.md): only apply decisions to the correct still-valid pending action. Receiving an approval or stop request at the worker is distinct from successfully applying it.

## 11.7. Queue limits and long outages

On connected computers, do not impose a platform storage quota on the pending-event queue or working files. The queue uses available disk space, subject to operating-system limits. Monitor usage and expose warnings when possible. A storage warning alone must not stop existing work or reject new jobs because of an artificial platform cap. Managed workspaces remain subject to their configured resource limits under section [14](resource-usage.md).

If logs are sampled, truncated, or dropped while handling actual storage exhaustion, retain a gap marker where possible and report the missing range. Do not imply complete retained logs or silently discard important events to keep routine streaming active.

Actual storage exhaustion creates a real trade-off: the system cannot guarantee both uninterrupted execution and lossless recording. If the worker cannot durably record acceptance of a new task, it must report the failure rather than acknowledge acceptance. The exact final fallback for existing work, warning thresholds, and whether particular jobs can be paused safely remain implementation decisions. The behavior must be explicit and must not claim successful durable recording when writes fail. Where even a gap marker cannot be written, report the uncertainty when storage or connectivity recovers.

## 11.8. File transfer recovery

Track file transfers independently of the small event queue. The queue may reference transfer identity and status; large attachment and artifact bytes should use the file-transfer mechanism rather than duplicate full payloads in event records.

Resume interrupted transfers where supported. Otherwise restart the transfer under the same logical identity without registering duplicate attachments or artifact versions. Verify the completed content before exposing it as ready.

Keep tasks waiting for required input files until execution-side readiness is confirmed. Mark artifact versions as centrally available only after their required upload and registration succeed, consistent with sections [#7](artifacts.md) and [#8](attachments.md).

Execution-computer-only storage must not silently become durable central storage as a retry fallback. Respect the selected content-storage policy during buffering and recovery.

## 11.9. Live IDE traffic boundary

Durable replay applies to task delivery and retained activity, not every IDE interaction. Section [2.7](connections.md#27-ide-connections-relay-first-direct-access-later) governs live editor, terminal, and preview connections.

Reconnect the IDE through its supported mechanisms and preserve unsaved browser edits. Do not blindly replay terminal keystrokes or uncertain file mutations through the event queue. Reconcile relevant state before retrying operations that may already have taken effect.

## 11.10. Recorded decisions and deferred implementation

**Recorded:** execution continues independently of the coordinator connection where possible; accepted tasks and outgoing events are persisted locally; task dispatch retries reuse the same ID; worker receipt ends dispatch retries; no acknowledgement-of-acknowledgement handshake is required; events leave the outgoing queue only after durable server acknowledgement; replay is deduplicated; reconnecting reconciles actual state; uncertain crashes do not trigger blind execution retries; interrupted files are verified before use.

**Implementation details still to choose:** queue/database technology, retry timing and backoff, acknowledgement batching, task-record retention, exact recovery state transitions, transfer protocol, and the final storage-exhaustion policy. The overall section [#11](recovery.md) design is agreed; these details remain implementation work rather than new product-flow decisions.
