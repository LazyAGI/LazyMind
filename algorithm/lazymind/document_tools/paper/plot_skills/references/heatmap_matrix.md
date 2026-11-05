# Quantitative matrices

Build the actual matrix from executed result rows and their scientific scope. The input has
complete result meanings and source tables; there is no host quantity_semantics or
pair_rectangle object. Use readable row/column labels generated from the variables and groups,
shared terminology, and actual comparison direction.
For multifactor categories, prefer short single-line level combinations, aligned factor
annotation columns or grouped headings/facets. Avoid a multiline factor-description
paragraph at every tick. Full names and shared factor definitions belong in the caption.
State a uniform n once; do not repeat identical n labels for each row. Show group-specific
support when it differs or affects interpretation. Keep every actual matrix cell and identity.
Use brief axis titles; put the ordered factor key and full metric definitions, including
derived-metric relationships, in the caption or a horizontal key rather than long ticks/titles.

Reuse draw_heatmap and draw_colorbar when applicable; their actual signatures and helper
source are supplied. Give the colorbar a separate axis and state the measured quantity/unit.
Choose layout suitable for the real matrix and label lengths. Do not overwrite the data axis
with a colorbar or copy reference labels and numeric values.

- matrix: supplied scalar cells; preserve missing cells as NA. Use viridis/cividis.
- difference: signed differences in compatible units; use a zero-centered diverging scale.
- correlation: actual supplied coefficients, method and scope; PuOr and limits (-1,1).
  Only a supplied/defined self-correlation supports a diagonal value. Missing pairs stay NA.
- correlation_pairs: a rectangular set of actual variable-pair coefficients; identify both
  variables and any cohort. Do not fabricate a full symmetric matrix.
- confusion counts/proportions: preserve the declared normalization and class order.
  Do not normalize already-normalized proportions a second time.

Related quantities can share a matrix only when numeric meaning and units permit it.
A common proportion scale does not turn a group mean into a contrast. Show important adverse
directions and do not use color to imply statistical significance.

For posterior events, use their actual event/parameter definition, limits (0,1) and probability
formatting that retains small values. Label the colorbar with the real probability event,
not a p value or posterior parameter mean.

The caption describes the actual matrix, missing cells, sample/interval meaning and simulation
status. Raw source observations do not justify uncomputed coefficients or uncertainty.
