# ppt-workflow

LazyMind AI PPT workflow: generate HTML slides in chat, preview in WorkflowPanel,
export PPTX (raster in browser by default; optional editable via Playwright).

## Layout

```
workflows/ppt-workflow/
  workflow.yaml / scenario/   # LazyMind workflow contract (entry / steps / UI)
  scripts/tools.py            # SubAgent tools (ppt_init_deck, ppt_run_stage, …)
  runtime/                    # HTML pipeline used at runtime (ONLY what we need)
    lib/model_client.py
    prompts/                  # style / outline / page-html / refine
    references/               # html constraints, style catalog, export-safe rendering recipes
    scripts/run_stage.py
    scripts/export_pptx/      # Playwright DOM → editable PPTX
```

## Note on SenseNova skills (important)

This workflow does **not** vendor the full SenseNova / OpenClaw skills tree
(`sn-ppt-entry`, `sn-ppt-creative`, `sn-ppt-doctor`, `sn-search-image`, SKILL.md
orchestration, workbench, etc.).

What we keep under `runtime/` is a **minimal runtime subset** adapted for
LazyMind:

| SenseNova skill piece | In this workflow? | Why |
|---|---|---|
| `sn-ppt-standard` run_stage + prompts | Yes → `runtime/` | HTML generation |
| `export_pptx` (Playwright) | Yes → `runtime/scripts/export_pptx/` | Shared by UI/API export and the explicit file-delivery tools |
| `sn-image-base` T2I runner | No | Removed; AI material images use framework `image_generator` in collect_materials |
| `sn-ppt-entry` | No | Replaced by `ppt_init_deck` + workflow scenario |
| `sn-ppt-creative` | No | Not used (standard HTML mode only) |
| `sn-ppt-doctor` | No | Env checks live in deploy / docs |
| `sn-search-image` | No | Replaced by `ppt_search_web_images` + `ppt_register_material_images` (Pool B → HTML `<img>`) |

Upstream SenseNova skills may evolve separately. Do not re-copy the whole
`skills/` tree into this repo; if a stage/prompt/export fix is needed, port the
specific file into `runtime/` and note it here.

## Material images → final HTML

`collect_materials` follows analysis when source evidence is required. It searches a supplied KB first and
uses web tools only for a concrete gap; it can complete without web access when
the request, uploads, or KB are sufficient:

`material_images` is optional. An unavailable image-search backend or an empty
image result does not fail collection or block outline/page generation; the deck
can use CSS, SVG, and ECharts instead.

1. KB/web/PDF candidates → `ppt_inspect_material_images` → select → `ppt_register_material_images`
2. **Only if the user explicitly asks** for AI-generated material images →
   `ppt_generate_material_images` (framework `image_generator`) → same Pool B
3. Every registered visual is published as an ordered `image` artifact, so the
   existing Materials layout shows preview cards with the original caption
   instead of local paths
4. `ppt_build_outline` auto-inits the deck and attaches them into
   `info_pack.user_assets.reference_images`, then publishes one Markdown `deck_outline`
5. `plan_page_prompts` publishes one complete editable `slide_outline` per page
   and a separate `speaker_notes_source` file
6. `ppt_generate_pages` runs asset-plan + batch-page-html (incl. UI edits) and
   embeds foreground `<img>`

Do not generate material images unless the user explicitly requests them.

## Opt-in AI slide backgrounds

Startup clarification asks whether the user wants a dedicated AI background for
every slide. The default remains the export-safe CSS/SVG path unless the user
explicitly enables it.

When enabled, background creation has a prompt stage and a generation
stage before outline planning. Host approval mode controls review pauses:

1. `plan_background_prompts` publishes one editable prompt per page without pausing for approval. Every
   prompt repeats the same visual-series anchor (palette, medium, light,
   texture, camera grammar, edge-detail strategy, recurring motif) while its
   page scene evolves with the deck narrative.
2. `generate_backgrounds` sends the approved prompts to the configured framework
   `image_generator`, writes `images/page_NNN_background.*`, normalizes to 1280×720, then publishes aligned
   `background_images`, and records `background_images.json`.
   Pixel inspection is advisory: report findings or unavailable checks without
   blocking publication or automatically regenerating usable files. Generation
   prompts still request no text; provider-added generation labels/watermarks do
   not fail the step.

The prompt step prepares the conversation-scoped deck first; `build_outline`
later reuses that same deck so approved prompts and generated files are retained.

Both steps are skipped when AI backgrounds are disabled. Requests such as
“重新生成底图提示词 1、2” or “重新生成底图 1、2” publish/overwrite only those
positions and preserve every unselected item. Page HTML uses the exact generated
file as its `#bg` CSS background; it remains separate from foreground Pool-B
`material_images`. Provider failures are terminal and include the failing page
and provider reason instead of silently falling back.

## Outline → HTML split

- `plan_background_prompts`: N editable, connected prompts (automatic)
- `generate_backgrounds`: N generated images from approved prompts (automatic)
- `build_outline`: **one call** `ppt_build_outline` → one Markdown `deck_outline` (automatic)
- `plan_page_prompts`: `ppt_publish_outline` → editable per-page `slide_outline` prompts (automatic)
- `generate_ppt`: **one call** `ppt_generate_pages` — **no** re-outline
- Low-level `ppt_init_deck` / `ppt_run_stage` / `ppt_publish_*` remain for
  debug, recovery, and single-page edits
- User can edit each page prompt in the Page Prompts tab; generate reads human revisions via `_resolve_artifact_text`

## Optional PPTX template

HTML is the default output. Startup never asks whether to use/upload a template
or to choose quick/full generation. An already supplied template is bound to
`template_pptx`; only then is the user asked for HTML or PPTX if unspecified.
Both template routes skip style questions and AI backgrounds. HTML recreates
the template style with components, without using full-slide background images.
Native PPTX composition can repeat, omit and reorder source layouts to meet the
requested page count. Template samples are not evidence; a prior work deck may also be evidence when
the user designates it as such. The selected route is saved as
`presentation_output_format`; missing route data requires resuming analysis.

When a template is present, `generate_ppt` calls `ppt_analyze_template` first.
The analyzer caches the original template plus a deterministic OOXML profile
(slide size, masters, layouts/placeholders, theme fonts and theme colors) and
applies its palette, typography and layout hints to the existing deck-wide HTML
style contract before page rendering. Whole-template reading happens in analysis; generation reuses and validates
the bound template before composition, without another visible checkpoint.

## Deck storage (conversation-scoped)

SubAgent workspaces are per task (`<root>/<user>/<task_id>/`), so decks and
material images are kept outside them, shared by every step task of one
conversation:

```
<upload_root>/workflow-workspaces/ppt-workflow/<user_id>/ppt_sessions/<conversation_id>/
  ppt_decks/<deck_id>/     # task_pack, outline, pages/, images/
  material_images/         # registered in collect_materials, attached at init
```

`upload_root` is resolved from `LAZYMIND_UPLOAD_ROOT` / `LAZYMIND_SHARED_UPLOAD_DIR`;
deployments may override only the PPT workspace with `LAZYMIND_PPT_STORAGE_ROOT`.
This location must be a persistent/shared mount. Without it, a follow-up edit
task would see an empty workspace after an executor/container replacement,
`ppt_find_deck` would find nothing, and the approved slide could not be edited.

## Single-page edit

Follow-up requests like "第3页删掉最后一个要点" patch one page instead of
rebuilding the deck:

1. `ppt_find_deck` (when `deck_dir` is not already known)
2. `ppt_read_page_outline(deck_dir, page=N)` — indexed bullets / data_points
3. `ppt_patch_page_outline(deck_dir, page=N, ops_json=[…])` — `delete_bullet`,
   `replace_bullet`, `insert_bullet`, `set_bullets`, `set_field`,
   `delete_data_point`, `set_data_points`; all ops for a page in one call
4. Make the slide match, either
   - `ppt_read_page_html(deck_dir, page=N)` for the element list and
     `html_sha256`, then
     `ppt_edit_page_html(deck_dir, page=N, ops_json=[…],
     expected_sha256=<html_sha256>)` — deterministic `delete_node` /
     `replace_text` on the existing HTML, no LLM redraw, and it republishes the
     page itself. Always pass the hash from the immediately preceding read; a
     stale edit is rejected instead of overwriting a newer page, or
   - `ppt_run_stage(deck_dir, stage='page-html', page=N)` — LLM redraw of that page

## Delete an entire page

Whole-slide removal ("删掉第3页", "去掉封面") is different from deleting a bullet:

1. `ppt_find_deck`
2. `ppt_delete_page(deck_dir, page=N)` — updates outline/asset-plan/background
   manifests, renumbers later pages on disk, and removes the matching UI list
   items (`slide_outline` / `background_prompts` / `background_images` /
   `preview_html` / `preview_notes`)

Do not re-run outline/style or regenerate untouched pages after a delete.

### Stable element ids

`page-html` tags every content element with `data-el` (`title`, `subtitle`,
`bullet-i`, `kpi-i`, `table`, `image-i`, `footer`) and pairs a heading with its
body via `data-group`. Those ids are the addressing layer for editing:

- `ppt_read_page_html` returns them as a small JSON element list (id, tag, text
  preview) instead of the HTML body
- `ppt_edit_page_html` deletes or retexts by `el` / `group`, so "只删这一项"
  never depends on matching text that may appear twice; replacement text is
  HTML-escaped and an ambiguous text fallback is rejected unless `all=true`
- the PPTX exporter carries each id into the shape name (`objectName`), so an
  exported deck stays id-addressable — `python-pptx` can drop one element by
  `shape.name` without regenerating anything

Decks generated before this existed have no `data-el`; `ppt_read_page_html` then
reports `repeated_classes` and edits fall back to `class` + `index`.

`ppt_init_deck` and the `outline` / `style` stages are for full generation only;
they discard the other pages' approved content.

### Why both step 3 and step 4

`outline.json` drives generation, the page HTML is what the user sees. Patching
only the outline leaves the current slide unchanged; editing only the HTML is
undone by the next redraw. `ppt_edit_page_html` warns when the outline still
carries text it just removed.

A stale count in `visual_hints` ("底部横向排列四个指标卡片") is the classic way a
deletion comes back: the rewriter keeps four columns and the generator invents a
filler card. `ppt_patch_page_outline` warns about this, and both page prompts
forbid padding a grid.

## Batch partial edits

After generation, requests such as “把第 2、3 页的卡片加宽”, “给这张表加一列负责人”,
“删除第 4 行”, “把要点改为编号列表” use the existing presentation revision.
No material collection, outline generation, or full slide generation runs.

| Operation | Native PPTX | HTML deck |
| --- | --- | --- |
| Position and size | Move/resize selected shape IDs; exact boxes in inches | Explicit CSS size/position; relative numeric inline dimensions |
| Layout | Align edges/centers, distribute, front/back, duplicate text/shapes/pictures | Scoped CSS changes and existing sibling insertion |
| Add objects | Native text box, list, editable table | Text, ordered/unordered list, table inside an existing container |
| List edits | Replace list; insert/change/delete items | Replace list; insert/change/delete items |
| Table edits | Change cells; insert/delete rows and columns | Change cells; insert/delete rows and columns |
| Delete objects | Delete selected shape IDs | Delete selected element IDs/groups |

`inspect_current_pptx` exposes shape IDs, boxes, table values and paragraphs.
`publish_pptx_edit(structure_edits=[...])` applies ordered operations in memory and
publishes one revision after validating the inspected source hash. Untouched ZIP
parts retain their bytes, including notes and font resources. Row/column insertion
keeps the table box and redistributes dimensions; existing cell styles are reused.
Merged tables reject structural edits. Removing the final row/column/item requires
an explicit whole-object deletion. New content may need a preview check for fit.

For HTML, inspect each page with `ppt_read_page_html`, then submit
`ppt_edit_pages_html(deck_dir, edits_json=[{page,expected_sha256,ops}])`.
A single operation may use `els` for several targets. Every page is validated
before writing; publication failure restores local originals and attempts to
republish them. Preview rollback failures are reported explicitly. This is
compensating recovery, not a server transaction across multiple page revisions.
Table/list indices are 1-based in the sequentially edited state. Delete multiple
indices in descending order. Preserve the outline when content changes so a later
user-requested redesign can reuse the revised content.

## Font replacement and portable exports

Font-only follow-ups patch existing content; they never call page generation,
rewrite the outline, move boxes or regenerate unselected pages. Examples:

- “全文字体换成 Noto Sans SC”：`ppt_replace_deck_fonts`, no `pages` filter.
- “第一页字体换成 Noto Sans SC”：`pages=[1]`.
- “全部标题字体换成 Noto Serif SC”：`text_scope=titles`.
- For selected text, inspect its current element/slot ID and patch only that selection.

For native PPTX, title scope means title placeholders; custom text-box titles
need inspected slot IDs. Chart/SmartArt parts and text baked into images are not
rewritten by the native text editor and are disclosed in its result.

Missing fonts from the 67-family cloud catalog download automatically; `allow_font_download=false` disables downloads. Other missing
fonts fail without changing the document. User-provided
TTF/OTF files can be imported with `ppt_import_font(filename=...)`; native edits
also accept `font_filename`. System fonts are copied into the managed library
only when their font metadata permits editable embedding. Restricted fonts and
fonts missing the selected text's glyphs are rejected.

HTML font edits contain full font data, so preview and editable export do not
need the original font library or network. Native PPTX edits embed full font
files without rebuilding slide content. Editable HTML-to-PPTX export validates
the required font parts and relationships before replacing the exported file;
failure preserves the previous export. This guarantees packaged font resources,
not identical rendering in every presentation application. Existing export and
artifact-size limits still apply; large fonts can cause an explicit size error.

## Defaults

- Preview: iframe HTML (`preview_html`), auto-published after `page-html` stages
- Page generation uses approved complete page prompts directly; rewrite prompts
  are reserved for requested edits.
- Page generation is sequential by default; publication preserves approved order.
- Export: UI export remains available. An explicit request for a PPTX file uses
  `ppt_export_deck` or `ppt_export_html` and publishes `native_pptx`. UI default =
  browser raster PPTX. Local/Desktop enables editable export automatically after
  its dependency bundle is detected; container deployments use
  `LAZYMIND_OUTPUT_EDITABLE_PPT=true`.


## Cloud font and presentation asset libraries

`Font/fonts-20261105.zip` and `Ppt-assets/presentation-assets-20261105.zip`
are hosted in the ModelScope `CarlosShaoting/lazymind-cst` dataset and mirrored
in Hugging Face `LazyAGI/LazyMind`. Font and image binaries are not bundled in
this workflow or committed to GitHub. The asset pack contains 318 named icons
from 12 collections, with original license notices and per-file checksums.

Preparing or resuming a PPT workflow starts background prefetch of both packs.
The request returns without waiting for downloads. The first consumer waits for
its pack if prefetch is still running. Downloads try ModelScope first, then
Hugging Face; failures report the mirror and reason, and later use retries.
Concurrent processes share a file lock. Exact byte count and SHA256 are verified
before safe extraction and atomic publication; partial downloads are discarded.

`LAZYMIND_PPT_RESOURCE_CACHE` overrides the disposable download cache (default:
`~/.cache/lazymind/presentation-resources`). Selected fonts are converted to
static faces in `LAZYMIND_FONT_DIR` for portable document embedding; downloading
the catalog does not install all fonts into the operating system. Completed
HTML/PPTX documents continue carrying their used fonts and asset license notices.

Resource versions, sizes, SHA256 and supported font names are pinned in
`algorithm/lazymind/presentation_resources.json`. Updating a pack requires
uploading identical versioned archives to both mirrors and updating that file.
Never commit archives or the extracted libraries to the code repository.
