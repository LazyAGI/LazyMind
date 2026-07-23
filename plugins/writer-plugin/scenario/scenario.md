# Unified AI Writer Plugin

## Scope

Use one artifact-backed WriterDocument workflow for compound creation and revision:

- read Feishu/Lark documents, uploaded files, and selected knowledge bases;
- generate an outline or use a supplied outline;
- generate, regenerate, or revise that same outline artifact;
- plan sections and write a complete document;
- generate, rewrite, or revise that same full-document artifact;
- publish a local outline or document to Feishu.

Do not route users between separate creation and revision plugins or expose separate
revision cards. The ChatAgent chooses the applicable mode inside the current product step.

## Steps

### prepare

Always begin a new workflow with `prepare`. It preserves the complete request, retrieves
requested sources, and constructs writing context.

Cloud document URLs are resource identity, not optional prose context. The trigger and
normalized request must preserve every source/destination URL supplied in the original
request or a clarification answer. Reading a document before triggering does not replace
passing its URL into the workflow. If the request refers to "this/my/original Feishu
document" but the consolidated request contains no locator, do not start an unbound
writing flow; require the missing URL.

### outline

`outline` owns the single user-visible `outline_ir` slot.

- First run with a supplied outline → prepare it as outline IR.
- First run without a supplied outline → generate it.
- User asks “change section X of the outline” → rerun `outline` and internally apply a
  PatchSet to the latest selected `outline_ir`.
- User edits in the frontend → the frontend saves a human revision of the same
  `outline_ir` slot.

Every result has stage="outline" and ui_editable=true. If the IR is bound to a cloud
document, AI or frontend revision synchronizes that document and stores the
provider-confirmed IR as the next artifact revision.

### write_document

`write_document` owns the single user-visible `final_document` slot and has two modes.

Generation/rewrite mode:

1. read the latest selected `outline_ir`;
2. regenerate section instructions;
3. draft all sections;
4. assemble and save `final_document`.

Targeted revision mode:

1. use the latest selected `final_document`, or `source_ir` for direct revision;
2. locate the requested content;
3. generate and apply a PatchSet;
4. save the result as the next revision of `final_document`.

Do not run section planning for a targeted body revision. Do run it again whenever the
body is generated or rewritten from a changed outline.

Frontend edits are human revisions of the same `final_document` slot. AI body revisions
remain local until `publish`, so generation and revision share the same write-back
boundary.

### publish

Use `publish` to write an unbound local `outline_ir` or `final_document` to Feishu, apply
a prepared body PatchSet to its original bound source, or publish to a new/different
target.

- A supplied target URI or an existing source binding can be written directly.
- “新建飞书文档” and “另存为” explicitly authorize creating a new target. Supplying
  a folder or wiki parent as the write location also authorizes creation there.
- A generic “写入/发布到飞书” does not authorize creation. If the local result has no
  target, ask whether a new Feishu document may be created and tell the user the result
  will be returned as a link. Enter or continue `publish` only after confirmation.
- Creation and writing are separate operations: persist `target_document` immediately
  after creation, then write to that exact target so a retry does not create duplicates.
- A targeted body revision of an existing document must publish its saved PatchSet.
  Full-document writing to that existing source is invalid because it appends retained
  blocks and can undo deletions.
- After a provider-confirmed write and read-back, return the browser URL as
  `published_link` so the frontend can display the completed Feishu document link.

## Supported paths

- From scratch: `prepare → outline → write_document`
- Supplied Feishu outline: `prepare → outline → write_document`
- Existing Feishu document revision: `prepare → write_document` in revision mode
- Outline only: `prepare → outline`
- Publish outline: `prepare → outline → publish`
- Publish local body: `prepare → outline → write_document → publish`

Repeated AI changes rerun/rewind `outline` or `write_document`. Repeated frontend changes
create human revisions in the same slot. Do not create a second document-version store or
a hidden current-document pointer.

## Artifact contract

- All structured outline and body results use the same WriterDocument schema.
- `outline_ir`, `final_document`, and `published_document` have ui_editable=true.
- `published_link` is the provider-confirmed Feishu/Lark browser URL.
- Internal locate results, modify plans, PatchSets, section plans, and draft blocks are
  persisted but are not exposed as separate product cards.
- Plugin tools pass artifact paths and do not copy complete documents into ChatAgent
  responses.

## Active-session intent mapping

| User intent | Step and mode |
|---|---|
| Read new sources or restart from changed requirements | `prepare` |
| Generate/use/regenerate an outline | `outline`, prepare/generate mode |
| Modify the current outline with AI | rerun `outline`, revision mode |
| Write/rewrite the body from the current outline | `write_document`, generation mode |
| Modify an existing/generated body with AI | rerun `write_document`, revision mode |
| Write an unbound local result to Feishu | `publish` |

When an outline change invalidates an existing body, rewind to `outline`; the next
`write_document` execution replans sections from the newly selected outline revision.
Use only step IDs currently reported as reachable by the runtime.
