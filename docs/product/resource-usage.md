# 14. Resource Usage and Optional Limits

## 14.1. User control

Users decide how much work to run on their own resources. Do not impose an arbitrary platform-wide concurrency cap or require a resource budget for every task. Existing operating-system, hosting, provider, and harness limits still apply; this decision does not promise unlimited capacity.

## 14.2. Connected computers

By default, add no platform CPU, RAM, GPU, storage, or agent/job concurrency limits to native execution. Users may ask the agent to reduce resource use, change how work is performed, or manage tasks themselves through available controls.

Do not impose a platform storage quota on the working computer, including its working files and pending-event queue. Available disk space and operating-system limits still apply. Section [11.7](recovery.md#117-queue-limits-and-long-outages) describes reporting and handling actual storage exhaustion; it does not introduce a configured platform storage cap for connected computers.

An instruction in chat is a request to the agent, not an enforced resource boundary. Preserve the native permissions and approvals from section [10](permissions.md). Resource warnings alone should not silently stop existing work or introduce an automatic busy-machine scheduling policy.

## 14.3. Managed workspaces

Provide configurable resource limits for managed workspaces, subject to available host capacity and permissions. Configured limits are enforceable settings, separate from conversational instructions to an agent. Their exact values, supported controls, and enforcement mechanisms are deferred.

Allow users to keep a managed workspace running, including for single-user installations. Automatic shutdown when idle is optional and configurable, not mandatory. Closing a browser or losing a connection is not sufficient evidence that a workspace is idle; active agents and background jobs must be respected under the lifecycle rules in sections [6](background-jobs.md) and [11](recovery.md). Exact idle detection and shutdown behavior are deferred.

Optional resource limits do not remove managed-workspace isolation or override host/provider restrictions.

## 14.4. Visibility

Show available resource-usage information and warnings so users can make informed decisions. Clearly distinguish observed usage, configured limits, and unavailable measurements. Exact metrics, warning thresholds, and UI controls are implementation details.

## 14.5. Recorded decisions and deferred details

**Decided:** no additional platform resource limits by default for connected computers; user-directed work management; configurable managed-workspace limits; optional idle shutdown with the ability to keep workspaces running; resource visibility and warnings.

**Deferred implementation:** numerical defaults, metrics, sampling, limit enforcement, exact settings, idle detection, and shutdown mechanics. No mandatory global concurrency cap or automatic busy-machine queue is introduced by this section. Existing task delivery and chat-input queues retain their separate purposes.

Backup, restore, and update scope is recorded in section [15](backup-and-updates.md).

