# Unified AI Writer Workflow

## Scope

Use one artifact-backed writing workflow for compound creation and revision. The same
steps operate on either Markdown or WriterDocument IR:

- read Feishu/Lark documents, uploaded files, and selected knowledge bases;
- generate an outline or use a supplied outline;
- generate, regenerate, or revise that same outline artifact;
- plan sections and write a complete document;
- generate, rewrite, revise, and explicitly deliver that same full-document artifact.

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

`outline` owns the single user-visible `outline_document` slot.

- First run with a supplied outline → preserve its Markdown or IR representation.
- First run without a supplied outline → generate it.
- User asks “change section X of the outline” → rerun `outline` and internally apply a
  PatchSet for IR or StringReplaceSet for Markdown to the latest selected outline.
- User edits in the frontend → the frontend saves a human revision of the same
  `outline_document` slot.

IR results have stage="outline" and ui_editable=true; Markdown results remain `.md`.
If the IR is bound to a cloud
document, AI or frontend revision synchronizes that document and stores the
provider-confirmed IR as the next artifact revision.

### write_document

`write_document` owns the single user-visible `draft_document` slot and has two modes.

Generation/rewrite mode:

1. read the latest selected `outline_document`;
2. regenerate section instructions;
3. draft sections in the outline's representation;
4. assemble the complete draft without changing representation;
5. save `draft_document`.

Targeted revision mode:

1. use the latest selected `draft_document`, or `source_document` for direct revision;
2. locate the requested content;
3. generate and apply a PatchSet for IR or StringReplaceSet for Markdown;
4. save the result as the next revision of `draft_document`.

Do not run section planning for a targeted body revision. Do run it again whenever the
body is generated or rewritten from a changed outline.

When the first full `.lmd` draft is derived from a cloud-bound Feishu source, generation
or direct revision writes it back once and replaces `draft_document` with the
provider-confirmed IR. Frontend edits and later AI body revisions are revisions of the
same `draft_document` slot and remain local until the user explicitly writes them back.
The initial provider write receives `resolved_media_assets` whenever the generated IR
contains Image WriterBlocks.

## Supported paths

- From scratch: `prepare → outline → write_document`
- Supplied Feishu outline: `prepare → outline → write_document`
- Existing Feishu document revision: `prepare → write_document`
- Outline only: `prepare → outline`

Repeated AI changes rerun/rewind `outline` or `write_document`. Repeated frontend changes
create human revisions in the same slot. Do not create a second document-version store or
a hidden current-document pointer.

## Artifact contract

- From-scratch and Markdown inputs remain Markdown. Feishu and `.lmd` inputs remain IR.
- `outline_document` and `draft_document` preserve that representation across steps.
- User-visible IR outline and draft documents have ui_editable=true.
- A successful Feishu write produces a new provider-confirmed `draft_document` revision;
  Markdown-to-IR conversion and provider result metadata remain internal to the step.
- Internal locate results, modify plans, revision sets, section plans, and draft blocks are
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

When an outline change invalidates an existing body, rewind to `outline`; the next
`write_document` execution replans sections from the newly selected outline revision.
Use only step IDs currently reported as reachable by the runtime.
