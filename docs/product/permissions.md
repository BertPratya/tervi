# 10. Permissions and Approvals

## 10.1. Purpose and execution modes

The permission model follows the workspace's execution arrangement. Connected computers preserve the native harness experience, while managed workspaces add platform-enforced isolation. Browser controls expose the actual available settings and approval choices rather than replacing every harness's permission model with one universal mode.

| Workspace type | Execution boundary | Approval behavior |
| --- | --- | --- |
| Connected computer | Run the native harness under the user's OS account, preserving its configured safeguards and sandbox protections. | Expose its supported native permission modes and approval requests through the browser. |
| Managed workspace | Run inside an isolated environment with explicit file, network, privilege, and resource boundaries managed by the platform. | Keep native harness approvals within the enforced environment boundary. |

Approval and isolation are different: approval determines whether an action needs a user decision; isolation limits what the process can actually access. A working directory alone does not prevent access to other files.

## 10.2. Connected computers: preserve native behavior

The platform should behave like remotely operating the selected native harness on the user's computer. Use the selected agent's native permission and approval system, whether it is Codex, Claude Code, or another supported harness, rather than adding a separate platform permission mode for local agent actions. It must not silently weaken configured safeguards, bypass approvals, elevate privileges, or require container setup merely to connect a workspace.

Run in the intended user's execution context so existing tools, credentials, and native configuration are available through supported mechanisms. Show the effective permission mode and access level in the browser. Unless enforced restrictions apply, a process running under the user's account may access files outside the project directory according to that account's permissions.

Expose harness-specific permission choices under section [5.5](conversations.md#55-supporting-additional-harnesses)'s capability model. Preserve their native meaning and describe their scope. Do not imply that identically labelled settings across harnesses provide identical guarantees. Unsupported management controls should be marked as unavailable or not yet integrated.

Platform authentication, machine pairing, and authorization of routed requests still apply. Native harness permissions do not grant the browser unrestricted access to every connector operation or service. Selecting a workspace folder does not create an additional read-only mode or sandbox for the local agent; its effective access follows the native harness and operating-system permissions.

Additional platform-managed isolation for connected computers may be considered later. It is not mandatory in the first version, and this does not remove sandboxing already supplied by the harness.

## 10.3. Managed workspaces: enforce isolation

The platform provides an isolated execution environment for managed workspaces. Configure its accessible files and mounts, network access, privileges, and resource limits explicitly. A container's existence alone is not proof that those restrictions are enforced.

Agents, tools, and local MCP processes must operate within the intended execution boundaries. Do not expose coordinator database credentials, unrestricted container-management interfaces, or unrelated host directories to workspace programs.

Native approvals remain active according to the selected harness configuration, but cannot silently expand the managed environment's access. Even a harness mode that performs actions without prompting remains subject to the enforced environment limits.

Expanding a managed workspace's access requires a separate, explicit settings change. Explain the affected scope and whether applying the change requires restarting the environment or active sessions. Do not present an ordinary tool approval as automatically granting new host mounts, privileges, or network access.

The precise isolation technology, operating-system support, enforcement mechanisms, and default resource values remain implementation decisions. The UI must describe only restrictions the deployed environment actually enforces.

## 10.4. Approval requests in the browser

Show approval requests in the originating chat, with a clear indication that the agent is waiting for a decision. Include the proposed action, target, and relevant details provided by the harness, such as a command, directory, file change, or external tool operation.

Expose the choices supported by that harness. Examples may include **Allow once**, **Deny**, or a broader grant, but do not promise those exact choices for every integration. A broader option must explain whether it applies to a command pattern, tool, session, or other native scope.

Link each request and response to the correct workspace, harness session, run/turn, and pending action. Do not apply an approval to an unrelated action, replacement session, or different harness after a switch. Maintain the actual native grant scope rather than silently converting a one-time decision into a persistent rule.

An approval allows the proposed action under the relevant policy; it does not prove that the action executed successfully. Display its later execution outcome separately.

## 10.5. Disconnection and request lifecycle

Never approve an action merely because the browser or execution computer disconnects, or because the user has not responded. Existing work may continue only where it does not require a new decision, consistent with section [#6](background-jobs.md).

Distinguish a pending request, a decision submitted by the user, and a decision confirmed as applied by the harness. If delivery is uncertain, show that state rather than reporting successful approval.

After reconnection, reconcile pending requests with the actual harness session. Show expired, cancelled, already resolved, or no-longer-valid requests accurately. Do not send stale approvals to a new action or automatically replay an external side effect. Detailed identifiers, acknowledgements, and duplicate handling belong in the synchronization section.

Harnesses may time out or cancel pending work; the platform must not promise that every approval request waits indefinitely. A recorded user decision remains history even if the corresponding action can no longer proceed.

## 10.6. Permission settings and history

Expose the selected harness's available permission modes and related controls through the browser. Show whether a changed setting is effective immediately, requires a new session, or is blocked by execution-environment restrictions. A saved setting is not proof that an existing session has adopted it.

Record approval requests, decisions, their scope, timestamps, associated session/action identifiers, and known delivery or application outcomes in platform history. Record relevant permission-setting changes so users can understand the conditions under which work ran. Avoid retaining secret values in approval details or logs.

The user should be able to distinguish:

- A native harness approval decision.
- A change to the harness's permission mode or persistent rule.
- A separate change to a managed workspace's enforced access boundary.

The initial product is single-user-first. Multi-user approval roles, delegation, and organizational policy administration are outside this section's initial scope.

## 10.7. Related features and limits

Section [#9](integrations.md) governs MCP and plugin configuration. Enabling an integration is not unconditional approval for all of its actions; preserve supported tool permissions and approval requests. A remote MCP service may act using credentials at that service, so local filesystem isolation alone does not define its external authority.

Section [#2](connections.md) governs authenticated routing, including IDE relay and future direct access. Direct access must retain its authorization checks. Agent approvals are not a substitute for IDE, terminal, or file-service access control.

Section [#6](background-jobs.md) governs stopping background jobs. Approving or denying one agent action does not automatically stop unrelated jobs. Section [#5](conversations.md) governs harness switching; previous decisions must not silently become grants for a different harness session unless their supported, explicitly stated scope permits that behavior.

## 10.8. Recorded decisions and deferred implementation

**Recorded:** connected computers run the native harness under the intended user's account with safeguards preserved; managed workspaces enforce isolation; browser controls expose native modes and approval choices; requests show action and target; decisions and their scope are retained in history; disconnection never implies approval; stale requests are reconciled; expanding managed access is a separate settings change.

**Deferred:** exact isolation technology and configurations, platform-wide defaults where native settings are absent, supported permission interfaces per harness, detailed approval-delivery protocol, and the design of any additional isolation mode for connected computers.
