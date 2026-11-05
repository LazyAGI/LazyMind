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

Main Chat owns requirement analysis and launch: understand the user's goal,
bind supplied template and source references, and trigger the Workflow. It does
not list work records, read document bodies, search local source directories,
compare evidence or select the largest contributions before launch. Preserve
those instructions for `collect_materials`. After launch, main Chat coordinates
Workflow steps and relays progress, questions and user feedback.

Uploaded PPTX templates are read inside `analyze_requirements`, before material
collection. This step reads every original page and additionally saves
`template_content_brief` (sections, slots, evidence requirements and gaps).
Collection and outline planning follow those sections. Template sample text is
not user evidence. Outline style preparation is preserved without a separate
A/B/C style-preview step.
Material collection and academic analysis share a conditional Materials tab;
effective executed steps remain visible even when a skip marker exists.
Template paths skip both background stages;
final generation revalidates the template and follows the saved output choice:
HTML recreates its style in components; PPTX composes pages from reusable layouts
with the requested page count. Neither route uses full-slide template screenshots
as backgrounds. With no template, HTML is the default. Only an already uploaded
template prompts for HTML/PPTX; never ask whether to upload/use a template.

1. `analyze_requirements` — goal, audience, length, visual style, constraints,
   and the user's explicit AI-background choice. A deterministic post-step gate
   checks the configured `image_generator` only when that choice is enabled.
2. `collect_materials` — runs when source material or research is needed; uses supplied KB first and
   calls web tools only for a concrete remaining gap; collects facts and may
   optionally register images (`ppt_search_web_images` +
   `ppt_register_material_images`, or `ppt_generate_material_images` only when
   the user explicitly asks for AI material images) so later HTML can embed them.
   Missing images never block the workflow; slides can use CSS/SVG/ECharts.
3. `plan_background_prompts` — when AI backgrounds are enabled, create N
   connected but page-specific prompts automatically.
4. `generate_backgrounds` — generate N images strictly from the approved
   prompts automatically. A request such as “重新生成底图
   1、2” overwrites only those positions.
5. `build_outline` — reuse the deck prepared above and call
   `ppt_build_outline` → one `deck_outline` Markdown artifact. This stage is automatic.
6. `plan_page_prompts` — publish `slide_outline[page1..]` for generation.
7. `generate_ppt` — one call: `ppt_generate_pages`; no outline rewrite. The
   generated slides are the final result.

Steps 3 and 4 are skipped together when AI backgrounds are disabled.
There is one staged generation route. Do not ask quick/full generation mode.
 Charts (including proportions),
icons and formulas use the shared HTML renderer in every scenario. Native
PPTX template filling retains its existing visual-editing limitations.

Academic scenarios collect and analyze sources in the same `collect_materials`
step, then continue to background/outline planning without a separate analysis
or reading-reflection step. The Materials tab displays one Markdown `material_summary`,
combining the source inventory and detailed analysis while preserving per-paper attribution, formulas,
figures, findings and limitations. Multiple attachments are parsed independently
to EOF and retain source IDs through comparison, synthesis and page prompts.
Before writing a multi-paper outline, collection records evidenced methodological
prerequisites and calls `ppt_plan_paper_order` to put prerequisite methods first.
The result and rationale live in `material_summary`; citations or benchmark relations
alone do not establish dependencies. Each paper retains problem/motivation,
method/process, and experiments/limitations, including when the user requests a
compressed deck. Page prompts and native PPTX composition follow that order.
General self-contained decks may
skip material collection according to the declared state transitions.
The collection step must use
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

After prompt generation, call:

```text
advance_step_and_hand_off(step_id="generate_backgrounds")
```

After background generation, call:

```text
advance_step_and_hand_off(step_id="build_outline")
```

After the automatic outline, advance to page-prompt planning; after prompts are published, call:

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
