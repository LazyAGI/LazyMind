---
name: diagram-design
description: Create branded architecture, IT current-state, flowchart, sequence, state machine, ER/data model, timeline, swimlane, quadrant, radar/spider, polar chart (polar/radial lollipop), loop/flywheel, nested, tree, org chart, layer stack, Venn, pyramid/funnel, treemap, bar, line, Gantt and scatter charts, high-level, process, medallion, data flow, DP integration, DP security matrix, Sankey, fishbone, Wardley map, kanban, user journey, deployment, dependency graph, UML class, story map, or database schema diagrams as standalone HTML/SVG/PNG. Redraw .drawio/.drawio.png/.drawio.svg or Mermaid .mmd sources at a chosen size/detail; onboard brand tokens from a website; add semantic patterns, callouts, accessible motion, or sketchy/hand-drawn styling.
license: MIT
metadata:
  version: "2.6"
---

# Diagram Design

Create self-contained HTML with inline SVG/CSS. Preserve user facts; ask for missing information that changes essential nodes, order or branches. Mark assumptions beside the diagram. The numbered sections below retain the original shared rules and reference targets. Do not preload all references or the full companion guide: use its detailed sections when needed. All original type references, scripts and assets remain available unchanged.

## 0. Style and project setup

Resolve the guide for each diagram; do not carry a selection across projects. This summarizes the generation path from `references/profiles.md`; its complete management and recovery procedures are unchanged.

- Inspect `<project-root>/.diagram-design` as data. Accept only one `profile: <slug>` line with optional horizontal whitespace and a final newline. Slugs match `[a-z0-9][a-z0-9-]{0,63}`; reject paths, extra keys, comments and prose. Ignore a malformed marker with a visible explanation and use markerless resolution.
- A valid marker selects `~/.diagram-design/profiles/<slug>.md` directly. Never copy it over the installed guide. If missing, explain and ask which profile to use; do not silently fall back. For `default` snapshot setup/recovery, follow the built-in-default procedure in `references/profiles.md`.
- Without a valid marker, read `references/style-guide.md`. A leading profile header identifies its active profile; a missing library entry does not invalidate the working copy, but report it and offer save. Without a header, compare all semantic roles and typography families to shipped defaults: differences mean custom-unsaved (offer save); unchanged values mean default. Do not infer customization from accent alone.
- For a selected profile, compare required semantic-role and typography rows against the current shipped schema. Backfill missing rows from pristine defaults in memory only, report the repair and offer update. Missing/malformed headings require asking whether to repair. Do not rewrite the named profile or working guide during marker-first generation.
- Read only the selected guide and the sections needed for this diagram. Profile save/load/list/show/update/reset/delete, onboarding, and failure recovery retain their complete procedures in the existing references.

With untouched defaults, no controlling marker and no prior explicit choice to use defaults, retain the original first-time brand setup gate (website, installed skill, local design system, manual tokens, use defaults, or saved profile). See the complete original §0 in `references/design-guide.md` when setup is needed.

## 1. Philosophy

Every node represents a distinct idea and every edge carries information. Merge concepts that always travel together; remove redundant labels/edges. Aim for density 4/10 and 1–2 focal elements. Split over-budget diagrams into overview and detail.

## 2. Use boundaries

Draw only when a visual teaches more than prose or a table. Use bullets/tables for lists or simple before/after, a sentence for a one-shape diagram, and wiretext for quick Unicode sketches. Do not force a diagram. Full selection and behavioral routing: `references/design-guide.md` §2–3.

## 3. Visual type guide and pre-draw plan

Always read the selected `references/type-*.md` before drawing. Common routes: components → `type-architecture.md`; decision/approval paths → `type-flowchart.md`; message order → `type-sequence.md`; role handoffs → `type-swimlane.md`. The complete 39-type routing table and behavioral-pattern routing remain in `references/design-guide.md` §3. Load `references/semantic-patterns.md` when behavior, state, enforcement or risk carries the meaning. State type/pattern, size and planned cuts; retain the original confirmation/assumptions rule in that section when these are not already fixed. Request independent references together where supported and reuse their contents for this diagram.

## 4. Universal anti-patterns

No gratuitous dark cyan/purple glow, shadows, identical treatment for every node, default equal-width summary-card grids, oversized rounded boxes (maximum 6–10px), vertical arrow text, floating legends inside the figure, unmasked edge labels, or accents on every important node. Do not reproduce an imported renderer's layout. Mono is for technical values, never blanket styling; never JetBrains Mono. Use the type-specific anti-patterns too.

## 5. Design system

Use named roles from the effective guide: `paper`/`paper-2` backgrounds, `ink` primary text/strokes, `muted`/`soft` secondary labels and arrows, `rule`/`rule-solid` hairlines, `accent`/`accent-tint` for at most two focal elements, `link` for HTTP/API/external edges. Node treatments and exact font roles come from the effective style guide; the table below retains the original role mapping. Preserve brand families/weights, disclose fallback fonts; otherwise names use Geist sans, technical values Geist Mono, title Instrument Serif, callouts italic serif. For CJK use the matching Noto/local fallback, 12px minimum; wide/full-width characters cost 1em, other characters their Latin advance, combining marks zero. CJK arrow/legend/tag prose uses 12px sans, weight 500, without tracking/uppercase; expand masks. Detailed language/series/terminal rules remain in `references/style-guide.md`, loaded only for those features. Series colors are only for charts that need them; terminal is a separate fixed opt-in skin.

| Role | Purpose |
|---|---|
| `paper`, `paper-2` | Page bg and container bg |
| `ink` | Primary text / stroke |
| `muted`, `soft` | Secondary text, default arrows, sublabels |
| `rule`, `rule-solid` | Hairline borders |
| `accent`, `accent-tint` | 1–2 focal elements per diagram |
| `link` | HTTP/API calls, external arrows |

### Node type → treatment

| Type | Fill | Stroke |
|---|---|---|
| **Focal** (1–2 max) | `accent-tint` | `accent` |
| **Backend / API / Step** | white | `ink` |
| **Store / State** | `ink @ 0.05` | `muted` |
| **External / Cloud** | `ink @ 0.03` | `ink @ 0.30` |
| **Input / User** | `muted @ 0.10` | `soft` |
| **Optional / Async** | `ink @ 0.02` | `ink @ 0.20` dashed `4,3` |
| **Security / Boundary** | `accent @ 0.05` | `accent @ 0.50` dashed `4,4` |

## 6. Core SVG primitives and connector rules

1. Off-axis connectors use rounded orthogonal elbows (`r=8`, minimum 6). Straight lines are only horizontal/vertical. Type-specific arcs/ribbons/diagonals need an explicit exemption in the type reference. Elbow and bridge formulas: `references/type-architecture.md`; code examples: `references/design-guide.md` §6.
2. Every arrow label has an opaque paper mask and a visible 6–10px gap from the stroke. Center it on a clear segment (beside vertical edges), not on the arrow. Keep Latin labels ≤14 characters; never vertical writing.
3. No connectors share a stroke or run on top of each other. Offset routes ≥12px; use bridge/hop for unavoidable point crossings. Redesign or split if arrows cannot be traced independently.
4. Multiple connectors on one node edge need distinct attach points ≥12px apart (8px only for tiny boxes). For N on edge length L use offset `L*k/(N+1)`, k=1..N. Do not merge strokes near the node; parallel arrows remain separated along their whole length.
5. Reroute around non-endpoint boxes. Only a geometrically unavoidable intervening cross-cutting box may be crossed: use a dashed transit stroke, label the visible end, and put the arrowhead at the true destination, never the intervening box.
6. A label mask must not overlap a node painted after it; the node would clip the text. Put masks on open canvas beyond node bounds. A badge fully inside its own node or a mask over an earlier zone container is allowed. Use the repository geometry verifier when available; installed self-check alone does not validate all geometry.

Draw arrows before node masks/boxes. Use opaque paper masks under translucent node fills, node `rx=6`, rectangular type tags `rx=2`. Define default/accent/link arrow markers. Keep the default background clean paper and borderless; dotted paper and framed containers are opt-in. Put the legend in a horizontal strip below the drawing, separated by a hairline, and add about 60px to the viewBox. Code and optional primitives stay in `references/design-guide.md` §6.

## 7. Layout and complexity

Use the 4px grid for coordinates, dimensions, padding and gaps (20/24/32/40/48px gaps, 8/12/16px padding); prescribed typography, 6px radii, stroke widths, opacity and 22px dot patterns retain their explicit exceptions. Default: at most 9 nodes, 12 arrows and 2 focal elements. Specialized budgets are in the selected type reference and `references/design-guide.md` §7, read only for the selected type when applicable. Imports have conditional `faithful` exceptions in `references/output-spec.md`; connector rules never relax. Header → borderless diagram → optional varied-width summary cards → hairline footer. Card/template details: `references/design-guide.md` §8/10.

## 8. Summary card pattern

Cards are an optional presentation choice. When used, retain the full original pattern in `references/design-guide.md` §8; vary their treatment rather than adding three identical cards.

## 9. Pre-output gate

Confirm type/pattern/size and assumptions; load its type reference. Check node/edge meaning, labeled decision exits, all paths ending or looping explicitly, selected complexity budget, ≤2 focal elements and a complete minimal legend. Remove redundant content. Check all six connector rules, legibility, viewBox containment, no clipped labels and grid alignment. Confirm font roles, brand/fallbacks, no shadows/oversized radii/equal generic cards. For imports report merged/collapsed/dropped content; never invent or silently omit source components. Run the installed self-check on the completed file before the final reply. Detailed upstream checklist: `references/design-guide.md` §1/4/5/7/9/12, only when a check needs elaboration.

## 10. Templates and variants

The default remains minimal light (diagram and title), static, with `assets/template.html` as the starting template. All original dark/full/consultant/sketchy/terminal/motion variants and selection rules remain in `references/design-guide.md` §10 and the existing assets/references. Read those details only when selecting that variant; examples are optional.

## 11. Importing existing diagrams

For draw.io or Mermaid input, follow the complete original §11 in `references/design-guide.md` and the matching `references/import-drawio.md` or `references/import-mermaid.md`. Existing extractor scripts parse source files, not prose requests; preserve the size/detail/fidelity and reporting rules. For prose requirements, author HTML directly.

## 12. Output and accessible SVG

Produce self-contained HTML with embedded CSS and inline SVG; The only allowed remote stylesheet is `https://fonts.googleapis.com/css2?...`, with local font fallbacks; omit remote preconnect links (including fonts.gstatic.com), external images and other remote resources. Static by default. Load `references/animation.md` only for requested motion or when ordered change materially benefits from it; motion must preserve the full static/no-JS meaning and honor reduced-motion/print/static-query states. Use the supplied motion controller, never improvise it.

The diagram SVG has `role="img"` and `aria-labelledby` pointing to both title and description. `<title>` is the first child before defs; `<desc>` follows. Use unique file/variant-prefixed IDs, never bare `title`/`desc`. Title names the subject (about ≤60 characters); description is one content-focused sentence, not shape narration. Decorative SVG uses `aria-hidden="true"`. Export only requested formats through `references/export.md`; do not mislabel HTML as image bytes. Publish through the chat artifact tool and return its exact link.

### LazyMind delivery

- Save the complete HTML using the available writing tools, then run the original `scripts/self_check.py <absolute-html-path>` (through `run_skill_script` when available). Repair failures and rerun the check before the final reply. Apply the original type/core checklist too; self-check does not prove all geometry/taste rules.
- In LazyMind, use `save_chat_artifact` to make the HTML available in the conversation. Follow its supported input modes: inline HTML can be saved as `text`, or an existing file as `file`. The save tool may also be the available writer that creates the file for self-check; it is not a separate mandatory filesystem workflow. If checking requires corrections, update the saved artifact and check the corrected file. Never claim a check passed unless it ran successfully. A workspace file alone is not delivery: include the returned `file_markdown` in the final reply. Once the diagram and checks are complete, give the link and a concise summary; further refinements require a user request.
