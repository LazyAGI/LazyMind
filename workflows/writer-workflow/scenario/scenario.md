# Unified AI Writer Workflow

## Scope

Use one artifact-backed WriterDocument workflow for compound creation and revision:

- read Feishu/Lark documents, uploaded files, and selected knowledge bases;
- generate an outline or use a supplied outline;
- generate, regenerate, or revise that same outline artifact;
- plan sections and write a complete document;
- generate, rewrite, or revise that same full-document artifact;
- deliver a local outline or document as Markdown, or publish it to Feishu.

Do not route users between separate creation and revision workflows or expose separate
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
3. draft all sections as Markdown;
4. assemble the complete Markdown draft and convert it once to WriterDocument IR;
5. save `final_document`.

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

### publish (delivery)

`publish` is the unified delivery step and always follows a completed document. Unless
the user explicitly requested a Feishu destination or write-back operation, export the
latest selected `final_document` as a downloadable Markdown file.

A Feishu URL used only as a source or reference is not a publishing destination. Use
Feishu delivery only when the request explicitly asks to update/write back the source,
publish to Feishu, create/save as a Feishu document, append to a target, or supplies an
explicit destination.

- Markdown delivery regenerates the `.md` file from the latest WriterDocument, so it
  includes revisions made after initial drafting.
- A targeted revision of the original body applies its saved PatchSet.
- A generated or rewritten body written back to its source replaces the source content.
- Append is used only when the user explicitly requests append or continue-at-end.
- “新建飞书文档” and “另存为” explicitly authorize creating a new target. Supplying
  a folder or wiki parent as the write location also authorizes creation there.
- An explicit request to write an unbound result to “我的飞书” authorizes creating a
  new document in the user's Feishu root without another confirmation.
- Creation and writing are separate operations: persist `target_document` immediately
  after creation, then write to that exact target so a retry does not create duplicates.
- A targeted body revision of an existing document must publish its saved PatchSet.
  Appending a full document to that existing source is invalid because it duplicates
  retained blocks and can undo deletions.
- When append versus replace is ambiguous for a non-empty target, ask the user rather
  than defaulting to append.
- After a provider-confirmed write and read-back, return the browser URL as
  `published_link` so the frontend can display the completed Feishu document link.

## Supported paths

- From scratch: `prepare → outline → write_document → publish` (Markdown delivery)
- Supplied Feishu outline: `prepare → outline → write_document → publish`
- Existing Feishu document revision: `prepare → write_document → publish`
- Outline only: `prepare → outline`
- Publish outline: `prepare → outline → publish`
- Publish local body: `prepare → outline → write_document → publish`

Repeated AI changes rerun/rewind `outline` or `write_document`. Repeated frontend changes
create human revisions in the same slot. Do not create a second document-version store or
a hidden current-document pointer.

## Artifact contract

- All structured outline and body results use the same WriterDocument schema and
  `.lmd` file suffix. Markdown draft sections remain internal `.md` artifacts.
- `outline_ir`, `final_document`, and `published_document` have ui_editable=true.
- `delivered_markdown` is the Markdown file regenerated from the latest selected
  WriterDocument for local delivery.
- `published_link` is the provider-confirmed Feishu/Lark browser URL.
- Internal locate results, modify plans, PatchSets, section plans, and draft blocks are
  persisted but are not exposed as separate product cards.
- Workflow tools pass artifact paths and do not copy complete documents into ChatAgent
  responses.

## Active-session intent mapping

| User intent | Step and mode |
|---|---|
| Read new sources or restart from changed requirements | `prepare` |
| Generate/use/regenerate an outline | `outline`, prepare/generate mode |
| Modify the current outline with AI | rerun `outline`, revision mode |
| Write/rewrite the body from the current outline | `write_document`, generation mode |
| Modify an existing/generated body with AI | rerun `write_document`, revision mode |
| Deliver without an explicit cloud destination | `publish`, Markdown mode |
| Write an unbound local result to Feishu | `publish` |

When an outline change invalidates an existing body, rewind to `outline`; the next
`write_document` execution replans sections from the newly selected outline revision.
Use only step IDs currently reported as reachable by the runtime.
