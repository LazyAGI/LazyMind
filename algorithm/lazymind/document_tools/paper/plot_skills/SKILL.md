# Paper figures from executed evidence

The application chooses figures after statistical execution. Each independent drawing
request receives the complete executed result table, original source tables, research
context, figure purpose/requirements, shared terminology/style, and relevant helper source.

Use the executed values and their actual scope, units, comparison direction and uncertainty.
Raw source rows support actual scatter/distribution/trajectory plots; supplied summary
statistics do not imply an unobserved sample. Missing cells and intervals are not zero.
Do not recompute tests, create intervals, invent observations or select favorable results.

Choose a useful geometry for the research question. Native helper source is supplied and
installed as global functions. Reuse those functions when they fit and call their exact
signatures. Ordinary Matplotlib code may implement a required geometry with the shared
appearance. Do not import skill scripts, copy example data, or rebuild interface validators.

Generate readable English axis labels, ticks, legends and annotations from the scientific
meaning. Internal result IDs and raw filter expressions belong in provenance, not on axes.
Use the shared terminology, series order and colors consistently across figures.
Identify every displayed series with a legend or direct label; derive legend handles
and labels together from the actual plotted artists.
Use concise category names; define scientific abbreviations and full conditions in the
external caption. State shared factor names, units and uniform sample support once.
For multifactor conditions use short single-line level combinations, aligned factor
columns, group headings or facets, instead of multiline paragraphs and the same n on every row.
Keep group-specific sample sizes when they differ or affect interpretation. Choose
this label hierarchy before wrapping; retain all conditions and comparison directions.
Keep axis titles brief; put lengthy factor-order keys and metric definitions in the
caption or a horizontal key instead of a long vertical title or tick label.
Leave font families and rcParams to the host; emphasize with fontweight. Export uses
a Unicode-capable font chain even if generated code overrides the family. Wrap long
comparison names or choose a horizontal layout while retaining the full meaning.
The caption explains how to read the plot, groups, units, sample/interval definitions
and provenance. Numeric contrasts and substantive conclusions are written once in
the separate figure analysis, rather than generated again in the drawing caption.
Do not silently change scales or denominators, and omit redundant dimensionless unit suffixes.

Keep the full figure title and caption outside the image. Preserve actual negative values
and interval endpoints. Differences need clear subtraction order and a zero reference.
Nonnegative bars use a zero baseline. Provide sufficient space for labels and legends.
Choose layout appropriate to the real data instead of copying sample margins or limits.
The host applies fonts and exports SVG/PNG/PDF; return a Figure without saving or showing it.
