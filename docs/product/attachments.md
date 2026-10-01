# 8. Chat Attachments

## 8.1. Purpose and scope

Users can attach images, documents, datasets, and other supported files to chat messages. Attachments are inputs the agent can inspect, edit, or use in computation. An edited or generated file becomes an artifact when delivered as a result, following section [#7](artifacts.md).

This section defines upload, storage, image preparation, delivery to the execution environment, and processing behavior. External tools, MCP, and plugins belong in a separate section [#9](integrations.md).

## 8.2. Storage policies

| Policy | Preserved original | Execution access | Availability |
| --- | --- | --- | --- |
| Central storage plus workspace delivery — default | Persistent attachment storage accessible through the central platform | Input transferred to the selected execution environment | Original can be accessed when the execution computer is offline; processing waits for execution availability. |
| Execution-computer-only — optional | Persistent storage on the selected execution computer | Input available there without retaining a central content copy | Uncached access and processing require that computer to be available. |

Under either policy, the coordinator retains attachment metadata, storage location, transfer state, and the link to the originating message. Central content storage may use a persistent disk or object store; file bytes do not need to be stored in the coordinator database.

Execution-computer-only storage is especially useful for large datasets because it avoids a persistent central duplicate. It does not guarantee that only one physical copy exists: uploads, temporary processing files, editable working copies, and backups can consume additional space. Read-only tasks such as training should read the preserved input directly where possible rather than copy it unnecessarily. Preserve the original when a task requires editing it.

Storage policy and network route are separate choices. In the initial relay architecture, execution-only uploads may pass through the central relay without being retained as central attachments. Any temporary buffering must have explicit cleanup behavior and must not become an undeclared durable copy. Direct transfer is not assumed to be available merely because execution-only storage was selected.

Expose the storage policy before upload, with a clear explanation of availability and copies. Exact upload limits and the threshold for recommending execution-only storage remain configurable implementation details.

## 8.3. Upload and delivery pipeline

1. The user attaches files to a message and selects or accepts the storage policy. Image preparation settings are available before submission.
2. Assign each attachment a stable identifier and record its original filename, type, size, and originating chat/message. Store files in an attachment-specific location so equal filenames do not overwrite each other.
3. Preserve the original at the selected storage location. Under the default policy, upload centrally before transferring the required input to the execution environment.
4. Transfer through the connector for a connected computer, or make the input available on the managed workspace's persistent storage. Confirm transfer completion and integrity before marking the file ready.
5. Supply the agent with attachment metadata and its authorized execution-side path. Do not automatically insert the entire contents of every file into model context.
6. The agent inspects the input using supported tools and selects how to process it. Use a working copy when modifying a preserved original.
7. Present requested outputs as versioned artifacts under section [#7](artifacts.md). Long-running processing uses the tracked-job behavior in section [#6](background-jobs.md).

The UI distinguishes uploading, waiting for transfer, transferring, ready, and failed states. A task that depends on a file must wait until it is ready at the execution location; a successful browser upload alone does not prove that the agent can read it.

If computer B is offline, default-policy uploads can be stored centrally and shown as **Waiting for transfer**. Hold the dependent task until transfer completes. With execution-only storage, require an available destination or leave the upload pending on the client with an honest status; do not silently retain the content centrally or promise completion after the browser closes.

Retry interrupted transfers without creating duplicate attachments. Detailed resumable-transfer, cleanup, and acknowledgement mechanisms belong in the synchronization design.

## 8.4. How the agent reads and edits files

Files are made available in the workspace rather than automatically converted into one large prompt. The agent may extract text, inspect samples, render pages, run OCR, or write and execute code, according to the available tools and permissions.

| Example request | Processing pattern | Possible delivered result |
| --- | --- | --- |
| Edit a Word document | Inspect document structure, modify a working copy, and check formatting. | Revised Word document. |
| Summarize a PDF | Extract text and inspect page images or use OCR when necessary. | Summary or report. |
| Edit a PDF | Use PDF tools or reconstruct affected content, then verify layout. Fidelity depends on the source document and tools. | Revised PDF. |
| Train a model from a CSV | Inspect columns and samples, prepare data, write training code, and launch a tracked job. | Requested metrics, charts, model files, or report. |

A training program can read the full dataset from disk while the model sees only useful samples, statistics, and results. Uploading a CSV does not require placing all its rows in context. File handling capabilities depend on the installed tools and harness; unsupported formats or missing dependencies must produce an actionable explanation.

Attaching a file does not itself authorize running executable content inside it. Processing follows the platform's execution and approval rules.

## 8.5. Image originals and model-facing copies

Preserve the uploaded image original. Prepare a separate copy for model input when resizing or other image preparation is requested. Previewing or downloading the original must not silently return a reduced-resolution derivative.

Image token usage depends on the selected model, processed dimensions, and supported detail settings. File size in bytes is a separate limit; compression alone does not guarantee fewer image tokens. Avoid assuming one token formula or resolution setting applies to every model and harness.

### User-facing configuration

Expose image controls in the UI with recommended defaults:

| Setting | Default and behavior |
| --- | --- |
| Quality preset | **Balanced**, with Economy and Detailed alternatives. |
| Preserve original | On; preprocessing changes only the model-facing copy. |
| Resizing | Automatic according to the selected model, preset, and supported adapter behavior; preserve aspect ratio. |
| Estimated image tokens | Show before sending when a reliable estimate is available, clearly labelled as an estimate. |
| Advanced dimensions | User-configurable maximum dimensions. |
| Advanced token budget | Estimated image-token budget per image and per message. |
| Settings scope | Workspace defaults with per-attachment overrides. |

Concrete preset dimensions and token targets remain model-specific implementation choices. Recalculate estimates when the selected model or preparation settings change. If no reliable model-specific estimate exists, show that limitation rather than an invented value.

When a chosen budget requires reducing detail, show the proposed processed size and let the user resize, select fewer images, or explicitly override the budget. Warn about potential loss of small text or diagram detail where applicable; do not claim reliable automatic readability detection. Never silently discard attachments or raise a user-selected budget.

These controls apply to the images the platform is preparing to submit. They are not a guaranteed limit on total image usage across every internal model call of an agent run.

## 8.6. Image history and harness capability boundaries

Avoid attaching every historical image again as new input on each message. Associate images with the message that introduced them and explicitly selected reuse.

An image previously supplied to a native harness may remain in its context even if the platform does not submit it again. Provider caching can affect repeated-input cost; harness compaction can change retained context. The platform must not promise that hiding or removing an attachment card removes the image from an existing agent session.

Adapters should report which image detail controls, token estimates, usage information, and context-management operations they actually support. Preprocess new image inputs where possible, but do not imply that a general model API parameter is automatically available through every harness wrapper.

Expose selective context removal only when supported. Otherwise, an explicit fresh native session with a text handoff can avoid automatically carrying the old image context forward, while preserving the visible platform history. Explain that this changes the agent's context and can lose visual detail. It is not a guarantee against the agent subsequently reading an accessible image again.

Supported session operations must be used; editing native session files to simulate context removal is not the normal integration path.

## 8.7. Attachment identity, access, and lifecycle

Attachments retain their originating chat/message association. Reusing an attachment should reference its identity where possible instead of requiring another upload of identical content. A matching content hash may permit storage reuse without merging unrelated message records.

Chat association does not by itself enforce file isolation. Once a file is available in a shared workspace directory, another agent session with access to that directory may be able to read it. Do not label an attachment private to one chat without an enforced access boundary. Whether to introduce strict chat-scoped attachment access remains an explicit open decision.

Apply workspace authorization to attachment previews, downloads, and execution-side access. Do not treat knowledge of a file path or attachment identifier as authorization.

Preserved originals, editable working copies, and delivered artifacts have different roles. Deleting or hiding a chat attachment must not silently delete an independently retained artifact. Conversely, central metadata alone does not preserve content that the user deletes from execution-only storage. Detailed attachment retention and deletion controls remain to be specified.

The input storage policy does not automatically decide the output policy: section [#7](artifacts.md) governs delivered artifacts. Make the distinction visible, especially when a local-only dataset produces results that will be copied centrally.

## 8.8. Recorded decisions and remaining details

**Recorded:** preserve originals; central attachment storage by default; optional execution-computer-only storage for large or locally retained inputs; deliver files to the execution workspace before dependent work begins; provide paths and metadata rather than loading all file contents into model context; use tools and code for processing; track long-running jobs; deliver requested outputs through the artifact system; configurable image preparation with Balanced defaults and per-attachment overrides.

**Still open:** strict chat-versus-workspace access boundaries, upload limits, supported file types and preview tooling, concrete image preset values, transfer implementation, attachment retention/deletion controls, and harness-specific context-management capabilities. These details do not block recording the agreed pipeline. External tools, MCP, and plugins are reserved for section [#9](integrations.md).

