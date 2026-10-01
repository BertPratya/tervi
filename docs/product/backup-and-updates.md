# 15. Backup, Restore, and Updates

## 15.1. Initial scope

Provide basic user-initiated backup and restore, with user-controlled updates. Clearly show what each backup includes and excludes. A backup of the central installation does not automatically protect files stored only on an execution computer.

## 15.2. Central backups

Include saved conversations, platform settings, and centrally retained attachments and artifact versions, together with the platform records needed to interpret them. The user must be able to identify backup status and coverage; do not describe an incomplete backup as complete.

Exact backup formats, storage destinations, consistency mechanisms, and handling of credentials or other sensitive settings are deferred. Do not imply that restoring settings automatically restores provider authentication on another computer.

## 15.3. Execution-computer and workspace files

Offer optional, separate backup of worker-side files and managed workspace data. Make its coverage explicit, including whether working directories and local session/recovery data are included. Preserve the distinction between source files, delivered artifacts, and native harness state.

Do not silently upload execution-only attachments or large local datasets to central storage as part of backup. Users choose whether those files are backed up and where. Central artifact snapshots do not substitute for a complete workspace backup.

## 15.4. Restore

Allow recovery onto the same installation or a new installation. Show the selected backup and its coverage, and require confirmation before overwriting existing data.

Explain unavailable worker files or components excluded from the backup. Restoring saved history does not itself resume a live process or authorize rerunning a task. Execution recovery continues to follow section [11](recovery.md); uncertain work must not be blindly repeated.

Exact restore flow, conflict handling, migration compatibility, and validation mechanisms are implementation details.

## 15.5. User-controlled updates

Notify users when updates are available and let them choose when to apply them. Initial scope does not require automatic updates.

Let affected agents and jobs finish before updating their worker or runtime by default. If an update requires interruption, explain the impact and obtain explicit confirmation before forcing it. Closing a browser is not a reason to interrupt work.

A coordinator update should preserve accepted worker execution under section [11](recovery.md); temporary coordinator downtime can interrupt browser access and event delivery. Explain component compatibility requirements before proceeding when coordinated updates are necessary.

Preserve saved data during routine updates. Exact packaging, version checks, migration strategy, and rollback mechanisms are deferred. Updating the platform must not silently replace separately managed agent harnesses or tools without making that scope clear.

## 15.6. Planned later capabilities and deferred implementation

**Later product capabilities:** scheduled backups and optional automatic updates. These are postponed, not excluded. Their scheduling, retention, and interruption behavior will be decided when designed.

**Deferred implementation:** archive formats, destinations, transfer methods, credential handling, consistency checks, installer/update commands, compatibility checks, and migration/rollback mechanics.

**Recorded scope:** manual central backup, optional separate worker/workspace backup, explicit coverage, restore to the same or a new installation, overwrite confirmation, update notifications, user-selected update timing, and protection of running work.

## 15.7. Remaining scope review

No fixed additional section count is required. Most remaining questions are implementation details already marked as deferred. Before implementation, resolve these remaining product boundaries in their existing sections:

- Section [8](attachments.md): whether attachment access is shared across a workspace or strictly isolated per chat.
- Sections [7](artifacts.md) and [8](attachments.md): user-facing retention and deletion behavior for saved artifacts and attachments.
- Across the specification: identify the first release and later milestones without dropping postponed capabilities.

The first version remains single-user as recorded in section [1](workspaces.md); multi-user collaboration is future scope. A release-scope checklist can be the next planning step rather than adding more feature sections automatically.
