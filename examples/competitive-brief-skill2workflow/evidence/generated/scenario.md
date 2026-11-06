# Competitive Brief — Scenario Documentation

## 1. Overview

`Competitive Brief` turns a single runtime request into two user-facing deliverables:

- a seven-section competitive brief in Markdown, and
- a one-page sales battlecard per competitor (or an explicit short note when no battlecard was requested).

The Workflow is deliberately task-agnostic. Competitor names, the caller's own company context, and the focus areas all come from the runtime request `{{user_input}}` plus the three optional structured text slots. There are no attachment slots: every step must complete from text alone, so no run can be blocked by a missing upload.

Research is best-effort. When the runtime exposes a web search or page reading tool, steps may use it and must record only the sources actually visited. When no such tool is exposed, the steps fall back to model knowledge and label the result as unverified with a freshness caveat. No step may invent sources, URLs, quotes, prices, or review themes.

## 2. Materials

| Slot | Type | Produced by | Visible to user |
|---|---|---|---|
| `competitor_names` | text (list) | user input | no |
| `company_context` | text | user input | no |
| `focus_areas` | text | user input | no |
| `request_brief` | json | `collect_request` | no |
| `competitor_research` | json | `research_competitors` | no |
| `positioning_analysis` | json | `analyze_positioning` | no |
| `competitive_brief` | text | `synthesize_brief` | yes |
| `battlecard` | text | `build_battlecard` | yes |

Every non-external slot is produced by exactly one step, and only the two final text slots are exposed in the UI.

## 3. Control flow

Linear and acyclic, with no retry or refinement loops:

```text
__start__ -> collect_request -> research_competitors -> analyze_positioning -> synthesize_brief -> build_battlecard -> __end__
```

Each step is reachable from `__start__` and can reach `__end__`.

## 4. Step `collect_request`

**Purpose.** Convert the free-form runtime request into an explicit, normalized research plan so that later steps do not each re-parse the request differently.

**Required inputs.**
- `{{user_input}}` — the authoritative task text: which competitors, any self-description of the caller's company, any focus areas, and any requested deliverables.
- `competitor_names` (optional structured list of competitors).
- `company_context` (optional description of what the caller sells, to whom, and how it differentiates).
- `focus_areas` (optional narrowing such as messaging, features, content, news, pricing, market presence).

**Produced output.** `request_brief` (json) with exactly these keys: `competitors`, `company_context`, `focus_areas`, `deliverables`, `assumptions`, `research_date`.

**Runtime behavior.**
1. Merge the competitors mentioned in `{{user_input}}` with the values in `competitor_names`, de-duplicate them, and preserve first-mention order.
2. If no competitor is named but the request describes a market segment or product category, use that category as the competitive set and record the substitution in `assumptions`.
3. Capture the caller's own company context when the request or `company_context` supplies it; otherwise set the field to `null` rather than inventing a baseline.
4. Use the stated focus areas when present; when absent, default to messaging and positioning, product and feature comparison, content and thought leadership, recent announcements, pricing and packaging, and market presence, and note the default in `assumptions`.
5. Record the requested deliverables, always including the competitive brief itself.
6. Stamp `research_date` with the current date in `YYYY-MM-DD` form.

The step must never ask the user a question and must never block on missing optional data; gaps become entries in `assumptions`. Its output is consumed by all four remaining steps.

## 5. Step `research_competitors`

**Purpose.** Gather structured, attributable evidence for each competitor in `request_brief.competitors`, prioritizing `request_brief.focus_areas`.

**Required inputs.** `request_brief`.

**Produced output.** `competitor_research` (json) with `research_date`, `mode` (`live` or `model_knowledge`), `confidence`, `freshness_caveat`, and a `competitors` array. Each competitor entry carries `name`, `overview`, `messaging`, `positioning`, `content_strategy`, `strengths`, `weaknesses`, `recent_developments`, and `sources`.

**Runtime behavior.**
1. Scope strictly to the competitors listed in `request_brief`; derive search keywords from those names plus the focus areas rather than from any fixed phrase list.
2. If a search or page-reading tool is exposed, use it on public pages such as homepages, pricing pages, newsrooms, blogs, and documentation, and record only the URLs actually visited.
3. If no such tool is exposed, do not simulate searching. Produce findings from model knowledge, set `mode` to `model_knowledge` and `confidence` to `low`, and state in `freshness_caveat` that no live sources were consulted and details may be stale.
4. For each competitor capture one-sentence positioning, target audience, size or stage signals, developments from roughly the last six months, primary tagline, core value proposition, three to five messaging themes, tone and voice, the problem it frames, product category, emphasized features, claimed differentiators, public pricing approach, blog cadence and topics, content formats, social and thought-leadership themes, SEO observations, strengths, weaknesses, and recurring complaint themes from public reviews.
5. Use `not found` for any field that could not be established. Never fabricate sources, URLs, quotes, pricing numbers, review themes, or dates.

Its output feeds `analyze_positioning` directly and is also re-read by `synthesize_brief` and `build_battlecard` for traceable detail.

## 6. Step `analyze_positioning`

**Purpose.** Turn per-competitor evidence into comparative analysis that a marketer or sales lead can act on.

**Required inputs.** `competitor_research`, `request_brief`.

**Produced output.** `positioning_analysis` (json) with `comparison_matrix`, `value_proposition_comparison`, `narrative_analysis`, `messaging_scores`, `content_gaps` (`topics`, `formats`, `opportunities`), `positioning_axes` (`axis_x`, `axis_y`, `rationale`, `placements`, `underserved_quadrant`), `opportunities`, `threats`, `recommended_actions` (`quick_wins`, `strategic_moves`), and `confidence_flags`.

**Runtime behavior.**
1. Build the messaging comparison matrix across primary tagline, target buyer, key differentiator, tone and voice, and core value proposition. Include a "Your company" column only when `request_brief.company_context` is present; otherwise state that no baseline was supplied.
2. Build the value-proposition comparison (promise, evidence, mechanism, uniqueness) and the narrative analysis (villain, hero, transformation, stakes) per competitor.
3. Score messaging from 1 to 5 on clarity, differentiation, proof, consistency, and resonance, each with a one-line justification.
4. Build content-gap tables for topic coverage, content-format coverage, and format-based opportunities, labeling every row as `gap for us`, `opportunity for them`, or `parity` strictly from the collected evidence.
5. Select the two most decision-relevant positioning axes for the market implied by the request, justify the choice, place every competitor in the quadrants, and name the underserved quadrant.
6. Derive opportunities, threats, and three to five recommended actions split into quick wins and strategic moves.
7. Keep traceability: every claim references a competitor entry from `competitor_research`, and any low-confidence or unverified item inherited from research is carried forward into `confidence_flags` rather than being silently upgraded. Do not introduce facts absent from `competitor_research`.

Its output is the primary source for both rendering steps.

## 7. Step `synthesize_brief`

**Purpose.** Render the user-facing competitive brief.

**Required inputs.** `positioning_analysis`, `competitor_research`, `request_brief`.

**Produced output.** `competitive_brief` (text, Markdown, exposed in the UI).

**Runtime behavior.**
1. Open with the research date and research mode taken from `competitor_research`. When confidence is low or `sources` is empty, add a one-line freshness and verification caveat instead of hiding the limitation.
2. Emit seven sections in fixed order: Executive Summary; Competitor Profiles; Messaging Comparison Matrix; Content Gap Analysis; Opportunities; Threats; Recommended Actions.
3. Keep the executive summary to two or three sentences plus the single biggest opportunity and the single biggest threat.
4. Write one profile subsection per competitor covering company overview, messaging analysis, product and solution positioning, content strategy, strengths, and weaknesses.
5. Reuse the matrix and gap tables from `positioning_analysis` rather than re-deriving them, and split recommended actions into quick wins and strategic moves.
6. Respect the caller's scope: when `request_brief.focus_areas` is narrower than the full framework, keep every section heading but compress out-of-scope sections into a short note instead of silently dropping structure.
7. Never invent numbers, quotes, or sources; use `not found` or `not tracked` where evidence is missing. Close with two or three follow-up options adapted to this specific run rather than boilerplate wording.

## 8. Step `build_battlecard`

**Purpose.** Produce the sales-facing one-pager that turns the analysis into talk-track material.

**Required inputs.** `positioning_analysis`, `competitor_research`, `request_brief`.

**Produced output.** `battlecard` (text, exposed in the UI).

**Runtime behavior.**
1. When `request_brief.deliverables` mentions a battlecard or a comparable sales-facing artifact, write a compact one-page card per competitor, or a single combined card when the request asks for a summary.
2. Each card contains a header with the competitor name and the last-updated date from `request_brief.research_date`, a quick overview, their pitch, their honest strengths, their weaknesses, our differentiators with proof, an objection-handling table (objection, response, supporting evidence), landmines to set, landmines to defuse, win and loss themes, and a maintenance cadence recommendation.
3. Build the "our" side only from `request_brief.company_context`; when that context is missing, state explicitly that the caller should supply their differentiators and proof points in a follow-up run.
4. When no battlecard was requested, still produce this slot exactly once: a short note stating that no battlecard was requested in this run followed by a one-line offer to generate one from the same research. Never return an empty value and never skip the slot.
5. Do not invent win rates, pricing figures, or review quotes; write `not tracked` or `not found` instead.

## 9. Degradation and failure handling

- **No company context.** Both analysis and battlecard steps proceed, add no baseline column, and state in the rendered output that the caller's own positioning details were not supplied for this run.
- **No focus areas.** `collect_request` applies the full default framework and records that default in `assumptions`.
- **No competitor names anywhere.** The competitive set falls back to the market segment or category described in the request, and the assumption is recorded so the reader can correct it.
- **No research tool exposed.** `research_competitors` switches to `model_knowledge` mode with `confidence: low` and a freshness caveat that propagates into the brief header and battlecard header.
- **No battlecard requested.** `build_battlecard` emits the short note and offer instead of failing or producing an empty slot.

## 10. Output placement

The two exposed slots are user-visible: `competitive_brief` is the full Markdown document and `battlecard` is the compact sales reference. The three internal JSON slots (`request_brief`, `competitor_research`, `positioning_analysis`) are hand-off material and are intentionally not surfaced to the end user.