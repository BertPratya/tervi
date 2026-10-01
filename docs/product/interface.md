# 4. Workspace Layout and IDE Experience

## 4.1. Purpose

The workspace should support different ways of working. Some users only want to chat with an agent. Others want to edit code, use terminals, and work through a full remote IDE. Users should be able to choose the tools they need without changing the underlying workspace or losing their conversation.

The interface will contain panels that users can arrange within a structured layout. Chat and the IDE are separate panels from the user's point of view.

**Decision: build the frontend around Eclipse Theia**, using custom React + TypeScript views. Deliver one unified interface supporting a ChatGPT-like chat experience and an IDE with chat. Detailed implementation is deferred.

## 4.2. Panels and structured positions

Users can move and resize panels, but the interface is not a free-form canvas. Panels snap into allowed positions, similar to the layout of a development tool such as VS Code.

The initial layout rules are:

- Provide defined docking areas, such as the left, right, center, and bottom.

- Show valid destinations when a user drags a panel.

- Resize panels by dragging the boundary between them.

- Keep minimum panel sizes so controls remain usable.

- Allow users to enable or disable optional panels.

- Do not allow panels to overlap arbitrarily or float anywhere on the page.

The exact docking areas, nesting rules, and support for grouping panels into tabs remain to be designed. The goal is flexibility within a predictable structure.

For example, a user could place chat on the left and the IDE on the right, then adjust their widths to 30% and 70%. Another user could give chat most of the space and keep the IDE in a narrower area.

## 4.3. Chat and the IDE

Chat and the IDE belong to the same open workspace. An agent and the IDE should work with the same project files on the selected execution computer or managed workspace.

| Panel | User experience |
| --- | --- |
| Chat | Send tasks, read responses, follow agent activity, and answer approval requests. |
| IDE | Edit files and use development features such as file navigation, editor tabs, terminals, and supported extensions. |

The IDE should behave as one panel in the outer workspace layout. Its internal tools can have their own arrangement. This is a product requirement, not a decision about how the panel is implemented.

Disabling the IDE must not disable chat or stop the agent. Users can ask the agent to modify files even when no editor is open.

Opening the IDE later should connect it to the current workspace. It should not create a separate copy of the project merely because the user changed the interface layout.

**Connection architecture:** section [2.7](connections.md#27-ide-connections-relay-first-direct-access-later) specifies central relay access first, followed by optional authorized direct IDE access with relay fallback. Both use the same workspace, login, and permission model. The route does not change the Theia application foundation or panel behavior. Chat Only establishes neither an IDE relay connection nor a direct IDE connection.

## 4.4. Optional IDE and resource usage

Users can choose Chat Only without loading optional IDE features, activating editors or terminals, or opening remote IDE connections. Load optional IDE features when the user enables the IDE. This choice should avoid unnecessary IDE downloads and live traffic for users who only want chat. Shared Theia application code needed for the unified interface may still load; this does not require loading the optional IDE features.

Hiding an IDE panel does not automatically stop its background activity. The implementation must distinguish visibility from whether the IDE is active.

| State | Meaning | Expected behavior |
| --- | --- | --- |
| Enabled and visible | The user is working with the IDE. | Load the IDE and maintain the connections needed for its features. |
| Temporarily hidden | The IDE remains active but is not currently visible, if this behavior is supported. | It may retain connections so it can reopen quickly. Do not describe this as disabled. |
| Disabled | The user has chosen not to use the IDE. | Avoid activating optional IDE features, or deactivate them safely and disconnect unnecessary IDE activity. Shared application services may remain loaded. |

These are behavior distinctions; they do not require three separate controls in the interface.

Disabling the IDE should reduce IDE-related browser traffic and resource usage. It does not eliminate chat or agent traffic, and the actual savings depend on what services were active. Switching to Chat Only after loading the IDE cannot undo earlier downloads; it should stop unnecessary IDE activity and connections.

Before unloading an active IDE, preserve unsaved edits or let the user decide what to do with them. Switching to Chat Only must not silently discard work.

Stopping the remote IDE backend is a separate lifecycle decision. Terminals or other tasks may depend on it. The platform must not terminate those tasks merely because their panel is no longer visible. The exact rules for retaining or stopping idle IDE services remain open.

## 4.5. Layout presets

Presets let users choose a useful arrangement without moving every panel manually. Users should also be able to save their own arrangements.

Initial preset examples are:

| Preset | Intended experience |
| --- | --- |
| Chat Only | Chat fills the main work area; the IDE is disabled. |
| Chat + IDE | Chat and the IDE appear together, with adjustable sizes. |
| Full IDE | The IDE gets most of the space; chat can be enabled when needed. |
| Custom | A user-saved selection of panels, positions, and sizes. |

Preset names and exact arrangements can be refined later. A preset changes the interface, not the project directory, conversation identity, or execution computer.

For example, a user can start a task in Chat Only, switch to Chat + IDE to inspect the changes, and return to Chat Only afterward. The conversation and agent session continue according to their normal lifecycle.

## 4.6. Locking the layout

Users can lock the layout to prevent accidental changes.

While locked, users cannot:

- Drag panels into new positions.

- Resize panels by dragging their boundaries.

- Close, remove, add, or disable panels in a way that changes the layout.

- Switch presets or reset the arrangement.

Users can still chat, edit code, scroll, use tools, and switch existing content tabs. Locking the layout does not make the workspace read-only or prevent an agent from working.

Provide a clear lock indicator and an accessible unlock control. Layout-changing actions should explain that the user must unlock first.

There are two possible scopes:

| Scope | Status |
| --- | --- |
| Outer workspace panels | Required: lock panel positions, sizes, and presence. |
| Internal Theia arrangement | Undecided: determine whether the same lock should also prevent rearranging editor groups and internal IDE views. |

Do not present the outer layout lock as freezing all internal IDE controls unless that behavior is implemented.

## 4.7. Remembering and restoring the layout

The platform should remember enabled panels, positions, sizes, and the lock state so users do not need to arrange everything again when they return.

Saved presets should remain available for reuse. Provide a reset action after unlocking so users can recover from an inconvenient arrangement.

The exact storage scope remains open: a layout may be a user default, a workspace preference, or a device-specific preference. Different screen sizes must be considered before choosing this rule. A desktop arrangement should not make the application unusable on a smaller screen.

Saving a layout is separate from saving project files and conversation history. Resetting the interface must not delete either of those.

## 4.8. Theia application foundation and customization

**Selected approach: build around Theia.** Chat, artifacts, and other platform views share one application with its IDE features. A separately embedded, untouched IDE is not the selected foundation.

The required experience includes:

- A ChatGPT-like Chat Only preset with conversation navigation, central chat and input, and optional artifact previews.
- IDE + Chat and custom layouts with movable, resizable panels.
- User controls to hide or show optional toolbars, menus, status bars, and other IDE controls, with a discoverable way to restore them.
- Consistent workspace identity, authentication, themes, and file-opening behavior.
- Preservation of unsaved edits when switching layouts or disabling IDE features.
- Chat and centrally saved history accessible when the execution computer or remote IDE is offline. New execution still requires an available worker.
- Agent and background-job execution independent of layout changes.

Theia supports custom React widgets and named layout perspectives. Perspectives are currently experimental; exact API choices and additional layout work are deferred. The desired appearance requires custom views and styling, not merely selecting a stock preset. [Widgets](https://theia-ide.org/docs/widgets/) · [Perspectives](https://theia-ide.org/docs/perspectives/)

The IDE remains a controllable area from the user's perspective; the exact grouping of its views is an implementation detail. Hiding controls does not itself unload services or stop processes.

React + TypeScript is selected. Vite is the selected build-tool preference, subject to compatibility with Theia's application tooling; it is not assumed to be a drop-in replacement for Theia's build pipeline. Integration details are deferred. Next.js is not selected.

## 4.9. Example experiences

**Chat-focused user:** opens a workspace in Chat Only, sends tasks, and reviews agent results. The IDE stays disabled unless the user chooses to open it.

**Developer:** uses Chat + IDE, places chat on the left and the editor on the right, adjusts the split, saves it as a preset, and locks the arrangement. Editing and agent work continue normally while the layout is locked.

**Remote IDE user:** selects Full IDE and works with files and terminals on the remote computer. They unlock the layout and enable chat when they want agent assistance, without changing the workspace.

## 4.10. Decisions recorded and questions left open

**Recorded direction:** build around Theia with React + TypeScript, customizable docked panels, optional IDE features and visibility controls, chat alongside the IDE, resizing, saved presets, layout persistence, and a lock against accidental layout changes.

**Deferred implementation details:** exact presets, docking and tab-group rules, visibility controls, internal IDE layout locking, preference scope across devices/workspaces, build-tool integration, and idle IDE backend lifecycle.
