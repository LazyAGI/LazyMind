# PDF paper reading

The original-PDF reader can parse formulas with the selected official MinerU service and show a LaTeX preview on hover. The formula marker also supports click and keyboard activation. The card exposes the recognized source for copying and opens a persistent study panel with explanations and original-paper evidence.

## Reading workspace

The paper workspace separates navigation, reading, and discussion. The left navigation contains the outline, references, and document vocabulary. Outline entries point to source blocks; they are not AI-written section titles. The center offers original reading, bilingual paragraph reading, and paper insight. The right panel retains the paper conversation and selected-text context. Its compact composer keeps the model selector and send/stop actions, while `@` mentions and `/` commands remain available from the editor.

Vocabulary uses the existing feature switch. Enable `LAZYMIND_VOCABULARY_ENABLED` in Core and `VITE_VOCABULARY_ENABLED` when building the frontend; enabling only one side is insufficient. Disabled deployments retain a clear vocabulary-tab explanation. Navigation and chat collapse on narrower screens, and formula/table/visual studies stack below the PDF when the reading area is narrower than 920 pixels.

Paper insight follows five stages: problem, key insight, method, experimental evidence, and limitations. Summaries are model interpretations. Their source links use server-validated block IDs and original coordinates, and related formulas or visuals open the corresponding study view. Missing support is shown explicitly rather than manufacturing a source. The guide uses the paper structure directly and does not wait for retrieval indexing or embeddings.

Bilingual reading is a reflowed reading view: a translation follows its original paragraph, with a link back to the original PDF. It uses the selected chat model and caches translations against source paragraphs. It does not overwrite PDF bytes or claim that the reflowed paragraph has the original page's visual geometry. Original PDF viewing remains available for checking equations, images, tables, and layout.

## Enable and use

Configure and select an official MinerU OCR credential in external-service settings. Open an original PDF in a knowledge document or paper attachment and enable formula parsing. Browser fingerprint validation requires HTTPS or localhost; insecure remote HTTP displays an explicit unsupported-context message. Parsing runs as a durable background job; the reader polls its status. Once ready, hover over an equation or activate its formula marker. Dragging to select document text keeps the existing text-selection behavior. Escape dismisses the formula card.

Parsing requires document write permission; existing results require read permission. The initial read does not upload the document. Local or custom MinerU services are currently reported as unavailable for this feature and are never silently redirected to the official service. Only the original PDF is supported; translated or regenerated PDFs must not reuse original coordinates.

## Formula study and navigation

Choose Explain on a formula card to request an explanation from the selected chat model. The panel presents its purpose, symbol meanings, interpretation steps, and exact source passages. Evidence buttons navigate to the original page and highlight the recorded region; Return restores the previous reading position. The formula catalog includes unnumbered equations. Inline `Eq. (n)`, `Equation (n)`, and `式 (n)` references navigate only when the IR contains a unique explicit matching formula tag. Ambiguous or missing numbers retain their original behavior. Native PDF internal links are intercepted only for a positively matched formula reference; external links and text selection remain available.

Explanations use bounded nearby PDF IR blocks, not retrieval results or embeddings. The selected model receives the recognized formula and those passages. Evidence text and coordinates are reconstructed from the IR on the server, never accepted from model-generated positions or quotations. Symbol associations and non-verbatim explanation steps are marked as interpretations even when they cite supporting text. This does not establish that the model's mathematical reasoning is correct.

The server caches explanations per user, source, parser, formula, context, selected model configuration, and language. Regenerate bypasses that cache. Closing the panel or switching formulas cancels the browser request. Access and source identity are checked again after generation. Source passages can contain MinerU markup, which is displayed as plain text rather than executed.

The complete rendered equation supports selecting semantic symbols by pointer or keyboard. Identifier subscripts, superscripts, and accents stay attached to their variables; compound exponent expressions expose their inner variables separately. A selected symbol opens its meaning and source quotations. If the general explanation omitted a symbol, selecting it requests a focused explanation of that exact source substring, with a separate cache and cancellation scope. Unknown or conflicting meanings are not guessed. Operators and punctuation retain their mathematical layout; unsupported or excessively nested syntax falls back to the original LaTeX. Only internally generated KaTeX interaction classes are trusted; PDF/model-supplied HTML commands are not enabled.

## Tables and related passages

Click an original table or its marker to open the table reader. Structured cells retain merged rows and columns, support keyboard navigation and selection, and display available source-designated headers without inventing header roles. The grid scrolls independently; source links locate and highlight captions or paragraphs in the original PDF. Formula and table studies share one panel and reading-position return behavior.

Table numbers and captions use the retained layout geometry. When MinerU groups several captions under one table, the projection requires a consistent caption side and an unambiguous adjacent table before reassigning them. Conflicting labels remain unnumbered. Explicit numbered references are linked only to unique table numbers. Where no explicit reference exists, a separate, unverified data-match clue may identify a paragraph containing at least three distinct decimal values, uniquely matching that table most strongly. Such clues do not establish a semantic relationship and are labeled separately from explicit references.

The additive `projection_version: 3` includes `block.table` with safe text cells, bounded `row_span`/`col_span`, captions, footnotes, optional number, and related block IDs. Caption source blocks include original page geometry. Existing source-matched IR can upgrade locally from its retained MinerU ZIP; every existing block is compared before an atomic cache replacement. This preserves original block IDs and avoids a new upload or parsing job. A missing or inconsistent ZIP leaves the older formula IR usable and reports an upgrade warning.

## Figures and contextual explanation

Numbered figures appear before unidentified image regions in the visual catalog. Image blocks carry captions, footnotes, optional numbers, and explicit paragraph references when attribution is unambiguous. Uncaptioned fragments remain regions rather than invented figure numbers. The guide and visual reader link to the original highlighted region; a table can also open its structured cells.

Visual explanations use recognized captions, table cells, and surrounding source passages. They do not analyze image pixels. Distinct observations cite authoritative source passages, and table comparisons request the reported dataset, metric, and matching experimental conditions. Unsupported visual details and statistical significance must not be invented.

## Retrieval preparation status

The PDF chat preparation banner describes the separate retrieval pipeline. A formula result can be ready while retrieval preparation is still running or has failed. The banner checks the current remote parser task instead of treating a historical local running flag as authoritative. Remote failures expose retry; a completed reparse with readable roots can recover from an earlier upload failure. Roots without a task version are not treated as completion while the current worker still reports working, since they can belong to an older parse.

If retrieval preparation fails, inspect the parser worker and its OpenSearch and Milvus dependencies. Restoring those services and explicitly retrying preparation is preferable to changing document states by hand. Compose uses restart policies for both stores; this does not remove their health or storage requirements.

## Independent document structure

The PDF IR subsystem owns original-source blocks, formula/table/figure geometry, and evidence-linked reading. Retrieval indexes, full-document translation jobs, translation-provider settings, and vocabulary storage/synchronization remain separate domains. The reading workspace embeds their existing entry points; geometry and evidence features must not mutate those domains' configuration or data. Changes to those domains should be integrated through their respective modules rather than duplicating their stores or provider configuration in the reader.

PDF IR does not call the retrieval parser, create text chunks, or update vector indexes. It does not depend on the document's retrieval processing status. The original PDF is snapshotted for upload without modifying the stored source. Its SHA-256 fingerprint binds the result to those exact bytes; the browser checks the displayed PDF against the same fingerprint before enabling formula hit regions.

The backend retains the raw MinerU ZIP and a versioned IR JSON under the document's artifact directory. These are durable files next to the uploaded document, so that directory must be on persistent storage. Internal filesystem paths and credentials are not part of the reader response. Job payloads contain document identifiers, source fingerprints and parser revision, not credentials. Jobs resolve the selected credential at execution time.

The IR preserves returned block types, normalizes equation blocks to `formula`, and includes LaTeX, source text, page and geometry. The raw ZIP remains available server-side for information outside the current reader projection, including original image assets. This is not a complete semantic graph of the paper.

Coordinates use `normalized_top_left`: `[left, top, right, bottom]` in `[0,1]`, relative to the original visible page with its intrinsic rotation. Page indices start at zero. Invalid or missing boxes are omitted instead of guessed. Native text selection does not provide formula identity; pointer hit testing uses the independent formula regions. Browser zoom and page scaling do not change stored geometry.

## API

`GET /api/core/datasets/{dataset}/documents/{document}/pdf-ir` returns the current result without starting a job. `POST` to the same path starts parsing; `{ "force": true }` requests a new parse after a completed result. An active job is reused. Responses use the `data` envelope and statuses `not_requested`, `queued`, `running`, `ready`, `failed`, or `unavailable`. A ready response includes `document`, with `version`, `source_sha256`, `parser`, `coordinate_space`, `pages`, `blocks`, and optional `warnings`.

`POST /api/core/datasets/{dataset}/documents/{document}/pdf-ir/formulas/{block}/explanation` accepts `{ "force": false, "language": "zh" }` (`en` is also supported). Its `data` contains `summary`, `symbols`, `steps`, authoritative `evidence`, `warnings`, and `model`. Evidence pages are zero-based and boxes use the IR coordinate space. Non-success responses expose an actionable `data.error_message`. The endpoint requires read access and a current source-matched IR.

An optional `focus_symbol` requests one symbol's meaning. It must be a nonblank, exact substring of the source formula, at most 500 Unicode characters. The response must contain one symbol; its identity is bound to the requested source substring and its meaning remains an interpretation. Focused and general explanations have separate cache entries.

`POST /api/core/datasets/{dataset}/documents/{document}/pdf-ir/guide` returns five ordered `stages`, authoritative `evidence`, `model`, `warnings`, and `source_sha256`. Each stage contains `key`, `title`, `summary`, `evidence_ids`, `related_block_ids`, and `inferred`.

`POST /api/core/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/explanation` supports image and table blocks and returns `summary`, `observations`, authoritative `evidence`, `model`, `warnings`, and `source_sha256`.

`POST /api/core/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/translation` translates an authoritative text block and returns `block_id`, `translated_text`, `language`, `model`, and `source_sha256`. Text blocks are limited to 8,000 Unicode characters. The bilingual view processes at most 20 paragraphs per page action, with two concurrent requests; individual paragraphs can be requested separately.

All three reading endpoints accept `language` (`zh` or `en`), `force`, and an optional expected `source_sha256`. The reader supplies its displayed PDF fingerprint. A changed source returns 409 before cached content or model output is used, and the browser also checks the returned fingerprint. Invalid hash values return 400. Translation, guide, and visual caches are separate and scoped to the authenticated user and source/model configuration.

## Limits and correctness

The reader targets detected standalone equations. Interactive symbols belong to the rendered study equation, not individual glyph hitboxes in the original PDF. It does not promise inline formula detection, complete cross-paper reasoning, or definitive links for references without unique identifiers. Recognition and rendering are separate: valid LaTeX can still contain an OCR error. Users should compare critical equations and table values with the original PDF. Missing geometry prevents hover rather than placing a misleading hit region. A mismatched source fingerprint prevents reuse and requires parsing the displayed original again.

The official parser currently uses the `vlm` model. Source PDFs are limited to 128 MiB, result archives to 256 MiB, and serialized IR artifacts to 32 MiB. An oversized IR fails explicitly instead of publishing a successful but unreadable artifact; an oversized projection upgrade preserves the existing usable artifact. Malformed formula syntax falls back to the original LaTeX without making the reader fail. Restarted or retried work may incur another service request; the remote provider does not share the application's job transaction.
