# 1. Workspace Storage and Execution

A workspace is the saved home for a project or ongoing activity. It supports chat, artifact generation, coding, and computation, bringing together files, agent conversations, settings, running work, activity history, and results. Users do not need to use an IDE or have a software repository to benefit from a workspace.

A workspace is not the same as a chat, a folder path, or a running container. Each workspace has a permanent ID. Its history stays connected to that ID even when its folder moves, its machine disconnects, or its execution container is replaced.

The platform supports two storage and execution arrangements:

1. **Connected computer:** the user selects an authorized directory on a computer they control; files and execution stay on that computer.
2. **Managed workspace:** the platform provides persistent storage and a managed execution environment, including a cloud worker with a mounted persistent volume.

Both arrangements offer the same core experience: chat, artifact generation, files, agent activity, background jobs, and results, with optional Theia IDE and terminal panels. Users can choose Chat Only to avoid loading optional IDE features and opening IDE connections, reducing unnecessary bandwidth use. Shared application code may still load as described in section [4.4](interface.md#44-optional-ide-and-resource-usage). The choice of storage and execution arrangement does not require a particular panel layout. Opening a workspace does not have to start an agent. Users can inspect or edit files first, then send a message when they want agent help.

The first version serves one user, who may connect several computers and create several workspaces. The platform should still record workspace ownership and keep access rules in one place so accounts and multiple users can be added later. Supporting multiple users will also require additional security work and testing.

A workspace does not require Git. It may contain a software repository, research papers, datasets, notes, or a mixture of files. A Git repository can be cloned into either arrangement. The repository's hosting service and the machine that executes the work are separate choices.

### Coordinator and Execution Services

The central server acts as the coordinator in both arrangements. It manages accounts and workspace ownership, saves conversations and activity history, routes requests, and relays authorized events and file access. It does not need to run agents or project computation itself.

A separate execution service launches agents and background jobs, manages processes, provides authorized file access, and sends logs and status updates. On a connected computer, this is the connector. In a managed workspace, this is the workspace service running with the managed worker. Both implement a common execution-service interface, while harness adapters handle provider-specific behavior.

| Arrangement | Working files | Agents and computation | Central server |
| --- | --- | --- | --- |
| Connected computer | Authorized local directory on the selected computer | Connector and processes on that computer | Coordinates requests, history, and access |
| Managed cloud workspace | Persistent volume mounted into the cloud execution environment | Workspace service and processes on the cloud worker | Coordinates requests and manages worker provisioning/lifecycle |

These are logical responsibilities, not a requirement for separate physical machines. A simple installation can run the coordinator and execution service on the same computer. Managed execution can also run on self-hosted infrastructure rather than a commercial cloud.

Initially, each workspace keeps its working files and computation together at one execution location. A mounted volume is a storage mechanism, not a third execution mode. Users choose **Connected computer** or **Managed workspace**, rather than choosing between a machine and a volume.

## 1.1. Connected Computer Workspace

### Purpose

A connected-computer workspace lets the user work with an existing folder on a connected computer. The platform uses that folder directly instead of requiring the user to upload a separate copy of the project.

The computer may be a laptop, desktop, or remote server. “Connected directory” means that the folder belongs to a connected machine; it does not mean that the folder is on the device displaying the website.

For example, computer A can host the platform server in the cloud, while computer B is the user's home desktop. The user opens the website from any browser device; A routes work to B, where the files, agent, artifact generation, and computation stay. B opens the authenticated connection to A, following section [#2](connections.md).

This lets users reuse hardware and tools they already own and can reduce cloud compute requirements. It does not eliminate electricity, coordinator hosting, transfer, or AI-provider usage costs. B must remain available for execution and live file access; saved server-side conversations remain accessible when B is offline.

### Creating and Opening a Workspace

The user follows these steps:

1. Connect a computer to the platform through a service running on that computer.
2. Give the computer a recognizable name, such as `Home PC` or `Research Server`.
3. Select an existing folder, create a folder, or clone a repository into an authorized location.
4. Confirm which folder the platform may access. Local agent file access and changes follow the selected harness's native permissions and approvals, as described in section [10.2](permissions.md#102-connected-computers-preserve-native-behavior).
5. Give the workspace a name and open it.

Initially, each workspace points to one root folder on one machine. The platform can support several root folders within a workspace later.

The browser does not gain direct access to another computer's files by knowing a path. The connected service must provide authorized access to the selected folder.

### Where Work Happens

When the user sends a message, the platform asks the connected computer to start or resume an agent session for that workspace. The agent performs its work on that computer, within the selected folder and the configured access limits.

The agent, Theia services when enabled, terminals, and project previews should use the same working files. For example, a file saved in the editor should be available to the agent without a separate upload.

Sharing files does not remove the possibility of conflicts. If the agent changes a file while the user has unsaved edits, the editor should warn the user and provide a way to resolve the difference. It must not silently discard either version.

The connected service sends messages, actions, command output, and run updates to the platform. The website displays these updates as they arrive.

### Example with Two Computers

One user may have these workspaces:

| Machine | Workspace | Root folder | Current work |
| --- | --- | --- | --- |
| Home PC | Website | `/projects/website` | Agent fixing a page |
| Home PC | Study Notes | `/study` | No active agent |
| Research Server | Simulation | `/research/simulation` | Experiment running |

The website should show all three workspaces and allow filtering or grouping by machine. Each entry should show the machine connection status, current activity, pending approvals, and last update.

Opening `Simulation` should show its saved conversations and current experiment. Starting a new conversation inside it should not create another workspace.

### What Remains Saved

The platform saves the workspace ID, name, owner, machine connection, folder location, conversations, important actions, approvals, and run history. Here, “platform” means the user's own platform installation, which may run locally or on a server.

The working files remain in the selected folder on the connected computer. Saving chat and activity does not automatically back up those files. Copying important results or creating file backups must be a separate, visible feature.

The platform should save each received activity event before forwarding it to the browser. The connected service should buffer events until the platform confirms receipt. Each event needs an ID so a repeated delivery does not create duplicate history. Buffering is subject to available disk space and operating-system limits, without a platform-imposed storage quota on connected computers; any lost activity should be reported.

Not every log needs to remain forever:

| Information | Intended retention |
| --- | --- |
| Conversations, approvals, action summaries, and run outcomes | Keep until the user deletes them or an explicit retention rule applies |
| Full command output | Keep for a configurable period, with an option to keep important logs longer |
| Important results and file checkpoints | Keep according to the workspace's storage settings |
| Internal troubleshooting logs | Keep for a shorter period and avoid storing secrets |
| Small pieces of streamed text | Combine into complete saved messages once delivery and recovery no longer need the individual pieces |

### Reopening or Changing the Folder

The workspace ID should stay stable when its location changes. A folder path helps find a workspace, but it is not proof that the folder still contains the same project.

| Situation | Expected behavior |
| --- | --- |
| The user adds the same folder again | Offer to open the existing workspace after matching the connected machine and folder location |
| Files change outside the platform | Refresh the visible files and flag affected results as potentially outdated when detectable; preserve past history |
| The folder moves or is renamed | Let the user locate the folder again and update the existing workspace's location |
| The folder is deleted | Show `Folder missing`; retain saved conversations and history, and disable execution until the folder is restored or reconnected |
| A replacement folder appears at the same path | When replacement is detected or identity is uncertain, ask whether to reconnect it or create a new workspace |
| The user archives the workspace | Hide it from the normal active list while preserving history and leaving the folder untouched |

Past runs should record the file versions or checkpoints they used when available. A reference to today's folder is not enough to explain yesterday's result. If the original files were not preserved, the interface should say so.

### Disconnection and Availability

Closing the browser should not stop work while the connected computer and its execution service remain available.

If the computer loses its connection to the platform, the website should show `Disconnected` and the time of the last update. Previously saved history remains readable. Live editing, new requests, and approvals that need the machine must wait for reconnection.

A lost connection does not prove that a job stopped. Work may continue if it can run without the connection. After reconnecting, the service should report the actual state and send buffered activity.

If the computer shuts down, its processes stop. The platform must not promise that every interrupted command can resume automatically.

## 1.2. Managed Workspace

### Purpose

A managed workspace lets the platform provide the project's storage and execution environment. The user does not need to select an existing folder on a connected computer.

The platform creates a persistent storage volume for the workspace and mounts it into an execution container. A volume is storage that remains after the container stops or is replaced. Mounting makes that storage available as a folder inside the container, such as `/workspace`.

The container provides the tools and processes. The persistent volume holds the working files. These two parts have separate lifetimes.

Managed workspaces can run on the user's own computer or on a server. “Managed” describes who handles storage and execution; it does not mean that the product requires a hosted cloud service.

### Creating and Opening a Workspace

The user follows these steps:

1. Choose `Create managed workspace`.
2. Enter a workspace name.
3. Start with an empty project, import files, or clone a Git repository.
4. Select an execution location if the installation provides more than one.
5. Open the workspace and begin working.

The platform creates the workspace record and persistent storage. It starts an execution container when the requested feature needs one, such as agent work, a live terminal, or IDE services. Reading saved conversations and previous run results should not require an active container.

When managed execution is introduced, its first implementation may provide one default execution location without exposing infrastructure choices to the user.

### Where Work Happens

The agent, Theia services, terminals, and programs operate on the files mounted into the workspace's execution environment. The platform manages that environment and applies its access and resource limits.

When the user sends a message, the platform starts or resumes an agent session in that environment. Messages and activity stream back to the platform and appear on the website.

Users can also edit files, run commands, inspect Git changes, and open results without asking an agent. The same file-conflict handling described for connected directories applies here.

### Example with a Persistent Volume

A user creates a managed workspace named `Data Analysis` and uploads `sales.csv`.

1. The platform stores `sales.csv` on the workspace's persistent volume.
2. A container mounts that volume at `/workspace`.
3. The user asks the agent to analyze the file.
4. The agent creates `/workspace/analyze.py` and starts a managed run.
5. The run produces `/workspace/results/chart.png` and a report.
6. The user closes the browser. The run continues while the execution machine remains available.
7. After work finishes and the environment becomes idle, the platform may stop the container.
8. When execution is needed again, the platform can start a container with the same volume. The input file, script, and saved results are still there.

Conversations and run history also remain available because they are saved independently of the container.

### Storage and Container Lifecycle

Stopping or replacing a container must not delete the workspace's persistent volume, saved conversations, or run history.

Files written only to temporary locations inside the container may disappear when it is replaced. The platform should make the persistent project folder clear and ensure important outputs are saved there or in separate durable artifact storage.

The platform should also save the environment configuration needed to rebuild the workspace's tools and dependencies. A package installed only inside a disposable container may not survive replacement unless the installation is captured in that configuration.

A persistent volume is not a backup. Recovery from disk failure, accidental file deletion, or a lost machine requires a separate backup or snapshot policy.

### What Remains Saved

Managed workspaces use the same history and log-retention rules as connected-directory workspaces. Their working files are additionally stored on platform-managed persistent storage.

Important generated outputs should be recorded as artifacts linked to the run or action that created them. When an older output must remain available even if its working file changes, the platform should preserve a separate version or copy.

Replacing the execution container does not automatically move storage to another machine. Moving a managed workspace between machines requires an explicit storage transfer or storage that both locations can access.

### Failure and Removal

If the execution environment fails, the platform should retain saved history and surviving persistent files, show the interruption, and provide a way to start a new environment. Recovering the environment does not guarantee that an unfinished program can resume from its previous state.

The interface should distinguish these actions:

| Action | Effect |
| --- | --- |
| Close the browser | Leave managed work running while its execution environment remains available |
| Stop the environment | Stop execution while preserving the workspace's persistent files and saved history; explain any effect on active work |
| Archive the workspace | Remove it from the normal active list without deleting files or history; handle running work separately |
| Delete workspace history | Remove the selected saved history without silently deleting working files |
| Delete workspace storage | Delete the managed working files after clearly showing the scope and consequences |

For either arrangement, the user should always be able to tell where files live, where work runs, what remains saved, and which machine must stay available.


## 1.3. Implementation Order and Future Hybrid Arrangements

Both workspace options are part of the intended architecture. Implement connected computers first to support personal use and existing hardware, then add managed execution, including cloud workers with persistent volumes, behind the same execution-service interface.

| Phase | Scope |
| --- | --- |
| First | Connected computers, authorized local directories, and a coordinator that may run locally or in the cloud |
| Later | Managed execution environments with persistent mounted storage and worker lifecycle management |
| Future hybrid option | Cloud-hosted working storage combined with execution on a connected desktop, subject to a separate synchronization design |

The initial design does not automatically synchronize a live cloud working directory with a desktop working directory. Splitting working storage and execution across locations requires explicit decisions about transfers, latency, caching, concurrent edits, conflicts, and offline behavior.

Saving chat history and job events on the coordinator while keeping project files on the execution computer is already supported; that is distinct from synchronizing the project files themselves. Optional artifact copies or backups also do not imply bidirectional working-directory synchronization.

Moving a workspace between connected and managed execution is not an instant selector change in the first version. It requires an explicit migration design for files, environment configuration, and supported agent state. Workspace identity and past history should remain stable when such migration is introduced.

## 1.4. Related Sections

- **[#2](connections.md) — System Architecture and Machine Connections:** connection topology and the connector's outbound connection.
- **[#3](agent-integration.md) — Agent Authentication and Remote Execution:** provider login and harness launching at the execution location.
- **[#4](interface.md) — Workspace Layout and IDE Experience:** optional IDE, Chat Only mode, and configurable panels; the frontend is built around Theia; customization details are deferred.
- **[#5](conversations.md) — Chat and Agent Interaction:** multiple conversations, harness adapters, queues, steering, and context handoffs.
- **[#6](background-jobs.md) — Background Tasks and Long-Running Work:** independently tracked jobs, stopping, completion, and disconnection behavior.
- **Future Connection Recovery and Event Synchronization section:** durable local event storage, acknowledgements, replay, deduplication, and recovery.
