# Document Reviser Plugin

## Scenario

Revise part of an existing Feishu document while keeping DocIR as the single
editing representation and Artifact revisions as the version history.

1. **load_document** — reuse the current user's chat-enabled Feishu OAuth
   connection, read the document, and store `source_ir`, `working_ir`, and a
   block-level remote snapshot.
2. **build_context** — build and display the revision context from the immutable
   source IR and the user's request without changing the document.
3. **revise_document** — locate the requested scope, generate a PatchSet, apply
   it to the selected DocIR, and expose every generated artifact to the frontend.
   This step deliberately pauses for user review.
4. **write_back** — after explicit confirmation, re-read the remote blocks,
   detect conflicts, and write only the changed supported blocks to Feishu.

Feishu is never modified by `load_document`, `build_context`, or `revise_document`.

## Intent Recognition

### Cold start

Invoke this plugin when the user provides a Feishu or Lark document link and
asks to modify the existing content. Preserve the exact user request when
triggering the plugin so the URL and revision instruction are not lost.

Examples:

- “把这个飞书文档的第二段改得更正式：https://example.feishu.cn/docx/xxx”
- “精简这份飞书文档中的背景部分，数据不要动。”
- “把这个飞书文档里所有第一人称改成公司口吻。”

Do not invoke it for new-document writing, read-only summarization, translation
without document mutation, or requests without a Feishu target document.

### Active session

While `revise_document` is waiting:

- A frontend edit creates a Human Revision of `candidate_ir`; do not run the
  algorithm merely because the artifact changed.
- An additional chat instruction resumes `revise_document`. The step reads the
  latest selected candidate, including human edits, and creates a new AI
  revision.
- “继续”, “完成修改”, or the Continue button resumes the step in confirmation
  mode. It saves `revision_confirmed` without regenerating the patch.

The revision interaction remains inside one resumable step. Do not rewind
`revise_document`: backend rewind invalidates outputs of the previous attempt,
including human revisions derived from `candidate_ir`.

## Feishu authorization

The plugin does not implement a second OAuth or error-mapping system. Core
injects tokens from the user's chat-enabled Feishu connection, and the plugin
preserves LazyLLM/Feishu FS errors for the platform authorization layer.

After the condition is fixed, retry `load_document`.

## Artifact contract

- `source_ir` is immutable and records the initially loaded document.
- `working_ir` is the first editable DocIR version produced by loading.
- `candidate_ir` is the selected version shown by the frontend IR control.
- Every algorithm rerun and every frontend edit creates a new Artifact revision.
- `remote_snapshot` points to the source DocIR used by LazyLLM for optimistic
  concurrency checking.
- `synced_snapshot` is created only after successful final write-back.

All structured artifacts are passed as file paths, following writer-plugin:
`get_artifact` returns a path, plugin-local tools read it, and their returned
paths are persisted with `save_artifact(content_type='file')`.

## Current write-back boundary

Writer IR blocks and their provider bindings must survive the complete round
trip. The LazyLLM adapter decides which structural operations are supported;
unsupported operations must be rejected instead of being flattened or silently
losing formatting.
