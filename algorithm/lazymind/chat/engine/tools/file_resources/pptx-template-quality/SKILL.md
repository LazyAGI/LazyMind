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

- Use ppt_fill_template pages to plan output order and count. Repeat source_page
  when content needs more room; honor an explicit user page count. Keep each
  reused page's layout, coordinates, typography, backgrounds and branding.
  Remove unused placeholders, sample charts/pictures/text boxes with explicit
  remove_shape_ids from inspection; never infer that all artwork is disposable.
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
  shrink fonts or remove required evidence to pass. Reposition or widen body
  boxes explicitly using shape_boxes when the slide has free space; keep brand
  artwork/title/footer intact. For balanced multi-column layouts, remove obsolete
  body shapes and use editable text_boxes with explicit bounds. Split supported
  content across repeated source pages when needed.
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

- For composition, use ORIGINAL template slot IDs in each page's edits, including
  repeated pages. Omit slots belonging to removed shapes. Review every remaining
  sample value; do not keep fictitious achievements. Supply fresh notes per page;
  composition clears sample notes and internal slide navigation.
- Use editable column/bar/line charts for supported performance comparisons;
  use pie/doughnut only for mutually exclusive parts of one known whole, with
  one nonnegative series and a positive total. Never turn unrelated improvements
  into pie slices or invent time-series observations. A key quantitative claim
  with complete evidence should have a chart, not only a large KPI number.
  Supply categories, named numeric series, unit, title, evidence source and box
  in inches. Delete the sample chart/body placeholder occupying the target area
  first. Preserve measurement scope, baseline and conditions in notes; never
  invent measurements or mix percentages with milliseconds. Chart citations are
  retained in notes. Use an available layout with sufficient space.
- Technical/research slides must preserve substantive on-slide explanations of
  root cause, concrete mechanism/steps, results and boundaries. Speaker notes
  expand these; do not hide all implementation detail there. Rebalance text and
  visual regions before deleting core technical copy. Keep evidence images and
  chart labels readable, preserve aspect ratio, and remove repeated/decorative
  content first when space is tight. Do not shrink text to bypass capacity checks.
- Native charts coexist with template artwork, icons and backgrounds. New icon
  artwork and formulas are not supported by this composition API; report those
  gaps precisely without discarding supported charts and registered pictures.
- Insert selected PDF/KB assets with pages.images [{material_index, box}] using
  the registered conversation pool. Remove obsolete picture placeholders first.
  Preserve the complete figure and labels; images fit without cropping/stretching.
  Use a repeated layout if necessary. Citations and PDF page metadata go into notes.
  PDF chart pictures are not editable numeric charts. Rebuild only with verified data.
