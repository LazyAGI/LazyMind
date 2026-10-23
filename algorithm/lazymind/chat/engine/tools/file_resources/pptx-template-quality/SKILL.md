---
name: pptx-template-quality
description: Fill an existing PPTX template with evidence-backed content while preserving its layout and checking text capacity before publication.
---

# Template filling quality

Read every page with pptx_analyzer. Its quality_contract is this skill and its
layout_checks describe whole text boxes, not independent run budgets.

In generate_ppt, consume the saved requirement_analysis, material_summary,
deck_outline and slide_outline. Do not search the web, knowledge base, PRs or
images again. If evidence is insufficient, report the missing material and return
to collection only with the user's agreement. Template analysis is local reading,
not a new research step. Cache hits still require reading the returned content.

- Keep the template's original page count, coordinates, typography and artwork.
  Resolve content against each box's effective layout, including inherited
  placeholder geometry and fonts. Unknown capacity is not permission to fill freely.
- Allocate content by whole text box before assigning text slots. A short styled
  run is not a container for an entire paragraph. Preserve bullet paragraphs and
  paragraph levels; avoid manual bullet characters and repeated blank lines.
- Prefer concise sentences and a few supported contributions over a PR inventory.
  Keep qualifiers, units and source attribution: an isolated 84% outline timing
  reduction is not an 84% end-to-end improvement. Separate merged and open work.
- Replace example identity/date values only with supported facts. If a required
  date is unknown, use a short explicit “日期待确认”, not an invented date or XX.
  Do not leave a required summary empty. Do not cram metadata into table headings.
- Keep essential citations compact. If notes editing is unavailable, retain
  detailed evidence in material_summary; do not pretend notes were updated.
- Before ppt_fill_template, compare all resulting text against layout_checks.
  Shorten over-budget content while retaining its meaning. Do not arbitrarily
  shrink fonts, widen boxes, add slides, or remove required evidence to pass.
- The tool rejects clearly over-capacity changed boxes before writing a file.
  Use its page/shape diagnostics to repair only affected text and resubmit the
  complete action list against the ORIGINAL template hash. At most two repairs;
  if still blocked, report the exact limitation instead of bypassing validation.
- Whole-paragraph replacements use newline-separated independent paragraphs;
  short rich-text run replacements retain inline breaks. Do not paste several
  unrelated sections into a subtitle. The tool resolves inherited typography.
- Geometry/text estimates are not visual validation. Validation belongs inside
  generation, before publication. Successful publication completes generation:
  neither the subagent nor parent chat should launch post-generation inspection.
  Report any returned limitations briefly without reopening the workflow or
  claiming native visual verification. A disclosure is not a pending task.
