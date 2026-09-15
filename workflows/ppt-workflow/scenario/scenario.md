# AI PPT Planner Workflow

## Scenario

This workflow helps users create a multi-slide presentation using the
**workflow HTML runtime** under `workflows/ppt-workflow/runtime/` (wrapped as SubAgent tools).

- Outline: **`ppt_build_outline`** (init → preflight → style → outline → one
  Markdown `deck_outline` containing every page description)
- Page prompts: **`ppt_publish_outline`** (one editable `slide_outline` item per page)
- Generation: **`ppt_generate_pages`** (asset-plan → batch-page-html using each
  page's `slide_outline` brief, including human UI edits; auto-publishes
  `preview_html`)
- Preview: full HTML pages in `preview_html` (iframe)
- Export: **not part of the skill**. User clicks the UI **Export** button

Workflow:

1. `analyze_requirements` — goal, audience, length, visual style, constraints,
   and the user's explicit AI-background choice. A deterministic post-step gate
   checks the configured `image_generator` only when that choice is enabled.
2. `collect_materials` — always runs after analysis; uses supplied KB first and
   calls web tools only for a concrete remaining gap; collects facts and may
   optionally register images (`ppt_search_web_images` +
   `ppt_register_material_images`, or `ppt_generate_material_images` only when
   the user explicitly asks for AI material images) so later HTML can embed them.
   Missing images never block the workflow; slides can use CSS/SVG/ECharts.
3. `plan_background_prompts` — when AI backgrounds are enabled, create N
   connected but page-specific prompts automatically.
4. `generate_backgrounds` — generate N images strictly from the approved
   prompts and stop for human image approval. A request such as “重新生成底图
   1、2” overwrites only those positions.
5. `build_outline` — reuse the deck prepared above and call
   `ppt_build_outline` → one `deck_outline` Markdown artifact. This stage is automatic.
6. `plan_page_prompts` — publish `slide_outline[page1..]` and stop for human
   approval so prompts can be reviewed.
7. `generate_ppt` — one call: `ppt_generate_pages`; no outline rewrite. The
   generated slide results also stop for human approval.

Steps 3 and 4 are skipped together when AI backgrounds are disabled.

After `analyze_requirements` succeeds, always advance to `collect_materials`.
This removes the ambiguous two-ready-step choice. The collection step must use
a supplied/selected KB before any web tool and must skip web retrieval when the
request, uploads, and KB already provide enough material.

After the first deck is generated, the user may continue chatting to revise
individual pages. Do not require them to restart the workflow.

## Intent Recognition

### Cold Start

Invoke `trigger_ppt_workflow(user_input=<user's exact original request>)` when the
user explicitly asks for a PPT/presentation/deck workflow or asks to create,
draft, plan, or structure a multi-slide presentation.

### Active / completed session (follow-up chat)

| User intent | Recommended step | Tool guidance |
|---|---|---|
| Change audience / goal / tone / constraints | `analyze_requirements` | full rerun of analysis |
| After analysis | `collect_materials` | mandatory next step; KB first, web only for a concrete gap |
| Add or update references/materials | `collect_materials` | KB/web facts and/or register images for HTML |
| Edit page briefs before HTML | (user edits Outline tab) | then `generate_ppt` |
| Replace all/specific background prompts | `plan_background_prompts` | publish only requested page objects for targeted replacement |
| Regenerate all/specific background images | `generate_backgrounds` | pass current approved prompts; targeted replacement uses `pages_json` + `replace=true` |
| Generate / regenerate HTML slides | `generate_ppt` | uses `slide_outline` briefs; no re-outline |
| Modify one or more specific pages | `generate_ppt` | **single-page edit** |

After analysis, call:

```text
advance_step_and_hand_off(step_id="collect_materials")
```

After materials are ready and AI backgrounds are enabled, call:

```text
advance_step_and_hand_off(step_id="plan_background_prompts")
```

After prompt approval, call:

```text
advance_step_and_hand_off(step_id="generate_backgrounds")
```

After background approval, call:

```text
advance_step_and_hand_off(step_id="build_outline")
```

After the automatic outline, advance to the page-prompt checkpoint; after page-prompt approval, call:

```text
advance_step_and_hand_off(step_id="generate_ppt")
```

#### Modify a specific page

```text
advance_step_and_hand_off(step_id="generate_ppt")
```

#### Delete an entire page

```text
advance_step_and_hand_off(step_id="generate_ppt")
```
