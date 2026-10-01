# 13. Installation, Deployment, and Technology Choices

## 13.1. Selected technologies

| Component | Decision |
| --- | --- |
| Central coordinator and platform backend | Go |
| Platform worker on connected computers and in managed workspaces | Go |
| Central server database | PostgreSQL |
| Browser application foundation | Eclipse Theia with custom React + TypeScript views |
| Frontend build tooling | Vite preference, subject to compatibility with Theia's application tooling |
| Theia IDE backend | Its existing Node.js / TypeScript implementation |
| Agent harnesses and external tools | Their existing implementations and runtimes |

The Go worker launches and manages companion processes; it does not rewrite Theia or agent harnesses in Go. Harness-specific helpers may use another runtime when required by the supported integration. The same worker codebase serves native connected computers and managed workspaces.

## 13.2. Central installation

Provide Docker Compose as the initial self-hosted installation path, on a user's server or a cloud VM. Docker can run on the cloud VM; cloud hosting and Docker are not alternatives. Preserve coordinator records and centrally retained files across routine restarts and upgrades.

Support both a single-computer installation and a central installation with separately connected computers. Components may share a host while retaining separate responsibilities. Exact infrastructure and installation commands are deferred.

## 13.3. Connected computer installation

Provide a native installer or precompiled executable for supported operating systems and architectures. Installation makes the worker command available on the user's `PATH`, so the user can invoke it from any directory in a terminal. Users should not need the Go compiler, Docker, or Docker Compose to install, connect, or run the worker on their computer.

The intended setup flow is:

1. Install the worker once on the computer, making its command available in the terminal.
2. Run the worker command from any directory and choose to connect to a central installation.
3. Complete the authenticated pairing flow initiated from the command. The flow may use browser-assisted authorization as described in section [2.4](connections.md#24-adding-a-machine).
4. Select workspace directories and configure agent authentication and required tools.
5. Run the worker in the background, with optional automatic startup.

One worker installation manages multiple directory workspaces. Do not require a new installation for each chat, task, or directory. Preserve native permissions and approvals from section [10](permissions.md), using the intended user's environment and credentials.

The worker initiates its authenticated outbound connection. Routine pairing should not require opening inbound desktop ports. The terminal command is the entry point for connecting the worker; its command name, subcommands or interactive menu, installer format, and background-service registration remain implementation details. Invoking the command from a directory does not automatically authorize that directory as a workspace.

## 13.4. Managed workspace installation

Include the worker in the managed workspace image or provision it automatically when its runtime is created. Users should not manually install it for every workspace or task.

A host-side runtime manager creates and manages isolated runtimes. The worker inside a runtime manages agents, jobs, and synchronization within that environment. These are separate responsibilities even when hosted on one machine.

A runtime can execute many tasks during its lifetime; a new task does not imply a new worker installation. Preserve the managed isolation requirements in section [10](permissions.md).

## 13.5. Companion tools and IDE services

Provide setup support for required companion components, including Theia's Node.js runtime and selected agent harnesses. Detect usable existing installations where appropriate and explain missing requirements. Exact bundling, download, version compatibility, and upgrade mechanisms are deferred.

Theia's workspace IDE backend runs beside workspace files. Go handles platform coordination and worker management. Remote IDE availability must not be required for reading centrally saved chat history.

## 13.6. Recorded decisions and deferred details

**Decided:** Go coordinator and worker; a Theia-based React + TypeScript frontend; Vite as the build-tool preference subject to compatibility; Docker Compose for initial central deployment; native installation once per connected computer with a command available on `PATH` and a terminal-initiated connection flow, without requiring Docker or Docker Compose on the worker computer; automatic worker inclusion in managed runtimes.

**Deferred implementation:** installer formats and commands, exact supported OS/version matrix, packaging, build integration, service registration, update mechanics, dependency provisioning, and detailed UI presets and controls. These remain future work, not excluded capabilities.

## 13.7. Related scope

Resource usage and optional limits are recorded in section [14](resource-usage.md). Backup/restore and update behavior can follow as a separate scope discussion. Detailed schemas, algorithms, APIs, and infrastructure design remain deferred.
