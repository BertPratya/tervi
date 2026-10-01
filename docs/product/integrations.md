# 9. External Tools, MCP, and Plugins

## 9.1. Purpose and integration principle

Users can extend agents with compatible third-party MCP servers and harness-native integrations through the browser. The platform should not require custom service-specific adapter code for every standards-compatible MCP server.

Reuse each harness's native MCP support where available. The platform provides a harness adapter for configuration, discovery, status, and supported interaction; that adapter is distinct from an adapter for each external service. Compatibility still depends on protocol versions, transport, authentication, and the MCP features the selected harness implements.

Follow section [5.5](conversations.md#55-supporting-additional-harnesses): provide consistent common controls, preserve harness-specific capabilities, and explicitly identify features that are not yet integrated or unavailable. MCP can expose tools, resources, and prompts; surface the features supported by the harness rather than assuming every integration offers only tool calls.

## 9.2. Adding third-party MCP servers

| Server type | Configuration | Execution and connection |
| --- | --- | --- |
| Local MCP server | Launch command, arguments, required environment variables, and any installation prerequisites. | Runs beside the harness on the connected computer or inside the managed execution environment, normally using standard input/output. |
| Remote MCP server | Server URL and required authentication. | The execution-side MCP client connects to the remote service over a supported HTTP transport. |

Once configured, the client discovers the server's advertised tools and input formats through MCP. Compatible servers should not require the platform developer to implement each tool separately. Generic controls can present tool names, descriptions, structured inputs, outputs, and approval requests. Specialized visual interfaces may require additional support and must not be promised for every server.

Installing or launching a local server executes third-party software. Require an explicit user action to install or enable it; receiving a server address or tool result alone is not installation authorization.

The exact installation workflow, supported package formats, dependency management, transport versions, and configuration schemas remain implementation decisions. A curated catalog is not required for adding a compatible server by configuration.

## 9.3. Browser management and existing integrations

Users should be able to manage both integrations already configured in their local harness and integrations added through the platform.

The browser should support, where the harness adapter permits:

- Discovering existing configured integrations.
- Adding a local server or remote endpoint.
- Editing configuration and completing required authentication.
- Enabling and disabling integrations.
- Inspecting connection status, discovered capabilities, and actionable errors.
- Seeing whether a configuration change requires reconnecting or restarting a session.

Clearly show each integration's origin, configuration scope, target harness, and execution location. Distinguish a platform-managed entry from one inherited from a native user-level or project-level configuration.

Do not silently overwrite existing native configuration or imply that disabling an integration in one chat removes it from all uses of the harness. When an inherited entry cannot be changed safely through the available interface, expose its status and explain the limitation. Exact precedence and conflict handling between platform and native configuration remain implementation work.

## 9.4. Workspace setup and chat selection

Configure platform-managed integrations per workspace by default. Within a workspace, let users choose which integrations are enabled for each chat where the harness supports that scope.

| Scope | Intended behavior |
| --- | --- |
| Workspace configuration | Defines available integrations and their settings for that workspace. |
| Chat selection | Chooses which available integrations the selected agent session may use. |
| Inherited native configuration | Shows the harness's actual existing scope and any restrictions on overriding it. |

A chat-level toggle must reflect actual execution behavior, not merely hide tools from the interface. If the harness does not support independent selection for concurrent chats, show the broader effective scope or mark chat-level selection unavailable. Do not simulate isolation by changing shared configuration behind other active sessions.

When users switch harnesses, re-evaluate compatibility and enabled integrations for the destination harness. Do not silently assume identical MCP support, credentials, or native plugin behavior. Explain any gaps before the user relies on those integrations.

## 9.5. Execution location and coordinator responsibilities

The execution environment makes MCP connections by default:

- For connected computers, local servers run on computer B and remote connections originate from the harness there.
- For managed workspaces, local servers run with the managed execution environment and remote connections originate there.
- The central coordinator manages authorized browser configuration requests, status, and relevant activity history. It does not need to execute or proxy every MCP tool call.

A local MCP server accesses the environment in which it runs, subject to its actual permissions. A remote MCP server performs work at its remote service; connecting to it does not automatically give it access to workspace files.

Central MCP gateways or shared centrally executed integrations are not required for the initial design. They would need a separate design for credentials, scope, and routing if introduced later.

## 9.6. Authentication and credentials

Keep integration credentials at the execution location by default, in protected storage appropriate to the harness and environment. For replaceable managed environments, provide credentials through protected persistent secret storage rather than baking them into images or project files.

The browser provides the setup and sign-in experience and shows connection status. The coordinator may relay setup information over authenticated channels where necessary, but should not retain secret values in ordinary configuration records, logs, transcripts, or tool-result history. Exact credential provisioning and OAuth callback routing remain implementation decisions.

Reuse supported authentication flows. Do not assume that credentials for one harness, machine, or remote service automatically work in another context. A workspace connection record and possession of valid service credentials are separate concerns.

## 9.7. Tool use, approvals, and lifecycle

An enabled integration makes capabilities available; it does not grant unconditional approval for every action. Preserve the harness's supported permission and approval behavior, with requests and responses accessible in the browser. Detailed platform-wide approval policy belongs in the permissions section.

Show meaningful tool progress, results, failures, and pending user actions under section [#5](conversations.md)'s activity-display rules. Avoid exposing secrets in these records. Large generated results can be delivered as artifacts under section [#7](artifacts.md) where appropriate.

Distinguish configuration changes from their effective runtime state. A saved setting is not proof that an active agent session has reloaded it. Show states such as connected, authentication required, connection failed, disabled, or restart required when applicable.

If applying a change requires stopping or restarting a session, make the effect on active work explicit. Disabling a tool for future use does not prove that an already-running external action was cancelled. Cancellation support and outcome must be reported honestly.

## 9.8. Native plugins and other harness-specific extensions

MCP integrations and native plugins are separate concepts. MCP provides a common communication protocol; a native plugin may bundle instructions, commands, hooks, MCP configuration, or other harness-specific features.

Preserve access to supported native plugin capabilities through the relevant harness adapter and browser controls. Do not promise that a plugin designed for one harness can be installed in another. Show its origin, effective scope, supported management actions, and any integration gaps according to section [5.5](conversations.md#55-supporting-additional-harnesses).

Exact plugin installation formats, catalogs, update policies, and supported feature inventories remain to be decided per harness. The common UI must permit harness-specific extensions rather than removing them to fit a universal plugin format.

## 9.9. Recorded decisions and deferred implementation

**Recorded:** compatible third-party MCP servers without per-server custom adapters; reuse native harness MCP support; manage existing and newly added integrations through the browser; workspace-level setup with chat-level selection where supported; execution-side local servers and remote connections; credentials kept at the execution location by default; visible compatibility and restart requirements; native plugins handled through their own harness adapters.

**Deferred:** exact schemas, config precedence and conflict resolution, installation commands and package handling, OAuth implementation, credential provisioning, discovery mechanics, protocol compatibility testing, specialized tool UI, and per-harness native plugin management details.

These details do not change the requirement to preserve supported capabilities and make limitations explicit. Section [#8](attachments.md) remains dedicated to chat attachments; external integrations are governed by this section.

