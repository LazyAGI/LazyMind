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
    references/               # html_constraints, style_catalog
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
| `export_pptx` (Playwright) | Yes → `runtime/scripts/export_pptx/` | Used by **UI/API** editable export only — **not** a skill tool |
| `sn-image-base` T2I runner | No | Removed; AI material images use framework `image_generator` in collect_materials |
| `sn-ppt-entry` | No | Replaced by `ppt_init_deck` + workflow scenario |
| `sn-ppt-creative` | No | Not used (standard HTML mode only) |
| `sn-ppt-doctor` | No | Env checks live in deploy / docs |
| `sn-search-image` | No | Replaced by `ppt_search_web_images` + `ppt_register_material_images` (Pool B → HTML `<img>`) |

Upstream SenseNova skills may evolve separately. Do not re-copy the whole
`skills/` tree into this repo; if a stage/prompt/export fix is needed, port the
specific file into `runtime/` and note it here.

## Material images → final HTML

`collect_materials` always follows analysis. It searches a supplied KB first and
uses web tools only for a concrete gap; it can complete without web access when
the request, uploads, or KB are sufficient:

1. KB/web hits → `ppt_register_material_images`
2. **Only if the user explicitly asks** for AI-generated material images →
   `ppt_generate_material_images` (framework `image_generator`) → same Pool B
3. Every registered visual is published as an ordered `image` artifact, so the
   existing Materials layout shows preview cards with the original caption
   instead of local paths
4. `ppt_build_outline` auto-inits the deck and attaches them into
   `info_pack.user_assets.reference_images`, then style/outline/publish
5. Outline assigns `use_image.reference_image_index`; UI gets per-page `slide_outline`
6. `ppt_generate_pages` runs asset-plan + batch-page-html (incl. UI edits) and
   embeds foreground `<img>`

Do not generate material images unless the user explicitly requests them.

## Outline → HTML split

- `build_outline`: **one call** `ppt_build_outline` → list slot `slide_outline`
- `generate_ppt`: **one call** `ppt_generate_pages` — **no** re-outline
- Low-level `ppt_init_deck` / `ppt_run_stage` / `ppt_publish_*` remain for
  debug, recovery, and single-page edits
- User can edit each page brief in the Outline tab; generate reads human revisions via `_resolve_artifact_text`

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
2. `ppt_delete_page(deck_dir, page=N)` — updates outline/asset_plan, renumbers
   later pages on disk, and removes the matching UI list items
   (`slide_outline` / `preview_html` / `preview_notes`)

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

## Defaults

- Preview: iframe HTML (`preview_html`), auto-published after `page-html` stages
- Page generation: one HTML-model call per page by default; set
  `PPT_PAGE_PROMPT_MODE=llm-rewrite` to restore the legacy per-page rewrite call
- Page concurrency: 4 by default (the tool accepts 1–8; lower it for rate-limited providers)
- Export: **click Export in WorkflowPanel** (not part of SubAgent tools). Default =
  browser raster PPTX. Local/Desktop enables editable export automatically after
  its dependency bundle is detected; container deployments use
  `LAZYMIND_OUTPUT_EDITABLE_PPT=true`.
