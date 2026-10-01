# 7. Artifacts, Results, and Previews

## 7.1. Purpose and terminology

An artifact is a deliverable explicitly presented to the user by an agent or background job. It can be newly created or an edited version of an existing file. Examples include a Markdown report, an edited Word document, an image, a chart, or a website project containing multiple files.

| Concept | Meaning |
| --- | --- |
| Working file | An editable file used during a task, including inputs, drafts, repository files, and scripts. |
| Artifact | A deliverable with a stable identity, linked to its workspace and originating chat or job. |
| Artifact version | A preserved snapshot of a delivered result, with its own version identifier. |
| Preview | A view of a selected artifact version; it does not replace the original downloadable content. |

A file may be both a working file and the source of an artifact. Not every file created or changed by an agent becomes an artifact: temporary scripts and ordinary repository edits can remain workspace activity. Explicitly presenting a result creates or updates its artifact record.

The platform supports artifacts without requiring Git or enabling the IDE.

## 7.2. Presentation and preview

Present artifacts as clickable cards or links in chat. Identify the artifact name, delivered version, and relevant file type. Keep a link to the originating job when applicable.

The default action opens a preview in a right-side dockable panel. This panel follows section [#4](interface.md)'s layout, resizing, presets, and locking rules. Provide an expand action, a download action, and an optional **Open in new window** action. On smaller screens, the preview can use a full-screen view.

The preview shows which version is open and provides a version selector and **View latest** action. Opening an older card must not silently substitute newer content.

Download returns the selected version's original content, not just its preview rendering. For multi-file artifacts, offer a downloadable bundle; its packaging format remains an implementation decision.

The preview should work independently of the full Theia IDE. Users may open supported working text or code files in Theia for manual editing. A preview does not imply a built-in editor for every file format. If a format cannot be previewed, offer its metadata and download instead of a broken preview.

The supported preview formats, rendering libraries, and exact controls remain to be selected. Generated websites or other executable previews require an isolated viewing design; a running application is distinct from a stored project snapshot. A source bundle being available offline does not guarantee that its backend-dependent live preview can run offline.

## 7.3. Editing and delivering updates

Both users and agents can continue editing working files. Users can request artifact changes through chat; supported files can also be edited manually in the IDE.

Use one stable artifact identity across revisions of the same deliverable. Its editable working file can retain a consistent filename, such as `report.docx`; users should not need to manage a sequence of names such as `report-final-v2.docx`.

When an agent delivers a revision:

1. Finish the intended changes to the working file or project.
2. Capture a consistent snapshot of the delivered content.
3. Register the snapshot as a new version of the same artifact.
4. Present that version in a new chat response or job-result message.
5. Preserve previous messages and their original version links.

Create versions at delivery boundaries, not for every intermediate edit or keystroke. Manual working-file changes do not silently rewrite delivered snapshots; the exact manual publish/checkpoint control remains open.

When editing an uploaded document, preserve the original input and use a working copy. Delivering an edited result does not erase the uploaded original.

## 7.4. Version history and restoration

Each delivered version is immutable. Subsequent edits modify working content and produce another version when delivered.

| User action | Expected behavior |
| --- | --- |
| Open the first delivery card | Open the version delivered in that message. |
| Open the revised delivery card | Open that newer delivered version. |
| Select View latest | Open the latest successfully delivered version. |
| Download an older version | Download that version's content. |
| Restore an older version | Use it as the basis of a new current version, retaining intervening history. |

Restoration must account for unsaved or newer working changes before replacing working content. It must not silently discard ongoing edits. Viewing an old snapshot alone never restores or changes workspace files.

An artifact can have any number of delivered versions, subject to storage limits and the user's retention policy. If there are n distinct delivered contents, a simple full-file implementation may store n snapshots plus the editable working content. User-visible versions and physical stored copies are separate concepts: identical content can share storage.

## 7.5. Storage and access

| Data | Default location |
| --- | --- |
| Editable working files | The connected computer's authorized directory or the managed workspace's persistent volume. |
| Artifact identity, version metadata, and chat/job links | The central platform's persistent database. |
| Delivered version snapshots | Persistent artifact storage accessible through the central platform. |

The default copies delivered snapshots to central artifact storage so users can access preserved results when the execution computer is offline. Artifact storage may be a local persistent store or an object store; it does not have to reside in the coordinator process or database.

Central copies apply to delivered artifacts, not automatic synchronization of every working file. The selected execution environment still performs edits and computation, following section [#1](workspaces.md). Downloading and previewing preserved snapshots do not require that environment to be running.

A version is centrally available only after its upload and registration succeed. If transfer is pending or fails, show that state and allow recovery; do not claim offline availability merely because an artifact card exists. The detailed transfer/retry protocol belongs in the synchronization design.

### Local-only option

Offer **Keep artifacts on execution computer only** for users who do not want central content copies. Preserve version snapshots on that execution computer rather than pointing every version at one mutable working file. The central server still retains artifact metadata and links.

Explain that uncached previews and downloads require the execution computer to be connected. Do not promise offline access based on temporary browser caching. Centralized previews that require uploading content must respect the chosen local-only policy.

Editing normally requires the artifact's execution environment to be available under either storage policy. Saved snapshots remain subject to workspace access permissions; an artifact link is not automatic public sharing.

## 7.6. Storage efficiency and retention

Begin with complete-file snapshots identified by content hashes. Reuse an existing stored object when its bytes are identical, while retaining the appropriate version records and chat links. If two files differ, whole-file deduplication alone does not save their shared portions.

For a multi-file artifact such as a website project, a version can store a manifest mapping relative file paths to stored content. Reuse unchanged files and store new content for changed files. A downloadable archive can be generated from the selected version; it does not require keeping a duplicate full archive for every version.

Git can continue managing source repositories and may be useful for code or Markdown history. The user-facing artifact/version model is independent of Git and must also handle Word documents, images, and other formats consistently. Do not automatically commit all artifact deliveries into the user's repository.

| Retention rule | Behavior |
| --- | --- |
| Default | Retain delivered versions until the user deletes them or enables an explicit retention policy. |
| Storage limits | Make limits and failed or blocked saves visible; reaching a limit must not silently delete history. |
| Configurable retention | Allow users to choose cleanup rules and understand their effect on older chat links. |
| Important versions | Allow versions to be pinned against automatic retention cleanup. |
| Removed version | Keep the historical chat reference understandable and mark its content as unavailable. |

Deleting a version must not remove shared stored content still required by another retained version. Detailed quotas, cleanup policies, and reference tracking remain implementation work.

Chunk-based deduplication, which reuses unchanged portions within files, is a planned optimization to evaluate later if observed storage usage justifies it. It is not required for the first artifact implementation.

## 7.7. Example: revising a Word document

1. The user uploads `report.docx` and asks the agent to improve it.
2. The platform preserves the input; the agent edits a working copy.
3. The agent delivers **Report — Version 1** in chat, linked to a saved snapshot.
4. The user asks to shorten the introduction.
5. The agent updates the same working document and delivers **Report — Version 2** in a new response under the same artifact identity.
6. The first card still opens Version 1. The second opens Version 2. Either preview offers version selection and download.
7. With the default storage policy, both delivered versions remain accessible if the desktop disconnects after uploads finish. Further agent editing waits for the execution environment.

## 7.8. Recorded decisions and remaining details

**Recorded:** artifacts include created and edited deliverables; right-side dockable previews; optional expansion/new-window viewing; downloads; stable artifact identity; immutable delivered versions; version-specific chat cards; preserved original uploads; central snapshots by default with a local-only option; explicit retention policies; whole-file deduplication and reuse of unchanged project files.

**Still open:** exact preview format support, safe rendering and live-preview execution, manual publication controls, storage backend, quota values, retention configuration, and detailed synchronization. These implementation choices do not change the agreed user-facing version model.

Section [#4](interface.md) governs panel layout, section [#5](conversations.md) governs chat and agent interaction, and section [#6](background-jobs.md) governs background-job execution and completion. This section defines how their delivered outputs are presented and preserved.

