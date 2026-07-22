# Document Reviser Plugin

## Scenario

Revise part of an existing Feishu document while keeping WriterDocument as the
single internal document representation and PatchSet as the modification contract.

1. **load_document** — reuse the current user's chat-enabled Feishu OAuth
   connection, read the document, and store it as the immutable `source_ir`
   WriterDocument.
2. **build_context** — build and display the revision context from the immutable
   source IR and the user's request without changing the document.
3. **revise_document** — locate the requested scope, generate a PatchSet, apply
   it to source_ir and keep the resulting WriterDocument as internal candidate_ir, translate
   the same PatchSet into provider-native block operations, write it to Feishu,
   and re-read the persisted WriterDocument.

Feishu is modified only by `revise_document`, after the local candidate is generated.

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

### Current interaction boundary

The current version executes one revision request from source document through
write-back without an intermediate confirmation branch. candidate_ir remains an
internal local artifact; the UI displays synced_snapshot re-read from Feishu.

## Feishu authorization

The plugin does not implement a second OAuth or error-mapping system. Core
injects tokens from the user's chat-enabled Feishu connection, and the plugin
preserves LazyLLM/Feishu FS errors for the platform authorization layer.

After the condition is fixed, retry `load_document`.

## Artifact contract

- `source_ir` is immutable and records the initially loaded document.
- `candidate_ir` is the internal WriterDocument produced by applying patch_set locally.
- `patch_set` is the only modification contract used for remote write-back.
- `synced_snapshot` is created only after successful write-back and is the IR displayed by the UI.

All structured artifacts are passed as file paths, following writer-plugin:
`get_artifact` returns a path, plugin-local tools read it, and their returned
paths are persisted with `save_artifact(content_type='file')`.

## Write-back boundary

Writer IR blocks and their provider bindings must survive the complete round
trip. The LazyLLM adapter decides which structural operations are supported;
unsupported operations must be rejected instead of being flattened or silently
losing formatting.

The revise_document tool passes source_ir and patch_set to WriterResourceTools.
It does not inspect NativePatchOperation and does not call FeishuFS directly.
