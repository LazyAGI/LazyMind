# Runtime notes

This directory is a **vendored, trimmed** copy of SenseNova `sn-ppt-standard`
pieces needed by LazyMind — not a full OpenClaw skill.

The vendored SenseNova portions are distributed under the MIT License; see
[`LICENSE.sensenova`](./LICENSE.sensenova).
Upstream project: [OpenSenseNova/SenseNova-Skills](https://github.com/OpenSenseNova/SenseNova-Skills).

Kept:
- `scripts/run_stage.py` — preflight / style / outline / asset-plan / page-html / refine
- `lib/model_client.py` — LLM/VLM hooks (LazyMind injects AutoModel via tools.py)
- `prompts/` + `references/` — prompts loaded by run_stage
  (`style_*`, `outline`, `page_html`, `page_html_rewrite`, `refine_*`)
- `scripts/export_pptx/` — Playwright helpers for **UI/API** editable export
  (not invoked by SubAgent tools; user clicks Export in WorkflowPanel)

Removed vs upstream skill:
- OpenClaw `SKILL.md` orchestration, workbench, progress WebUI wrappers
- Unused prompts (deck_review, page_review, asset_plan, …)
- Sibling skills (entry / creative / doctor / search-image)
- Decorative T2I (`gen-image` / `sn-image-base`); AI material images use
  framework `image_generator` via `ppt_generate_material_images` in collect

`asset-plan` still writes an empty per-page slot stub because `page-html`
requires `asset_plan.json` to exist; it no longer plans T2I images.

Do not re-expand into a full `ppt_skills/` tree. Port individual fixes only.
