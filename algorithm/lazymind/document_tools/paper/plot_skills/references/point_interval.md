# Point estimates and supplied intervals

Use this geometry for executed estimates, contrasts, coefficients or posterior quantities.
Every result row supplies its meaning, value, source scope, unit and optional interval.
Choose readable categories and series from those meanings. The drawing model writes the
visible labels; no comparison coordinate or host-prepared panel interface is required.

The installed draw_point_intervals helper takes summaries[method], with one
[estimate, lower, upper] triple per category, plus aligned methods/colors/edges/markers,
categories, title, xlabel, xlim and category_styles. Its exact source signature is supplied.
methods contains actual data keys and summaries stays indexed by those same keys.
The optional keyword-only method_labels maps these keys to visible legend labels; it never
changes data lookup. Reuse data.shared_style.terminology rather than creating another mapping.
For example, after building summaries from this panel's actual selected rows:

```python
methods = list(summaries)
draw_point_intervals(ax, summaries, methods, categories, colors, edges, markers,
                     title, xlabel, xlim, None,
                     method_labels=data['shared_style']['terminology'])
```

The global series_order is an ordering hint across figures, not the set of observations
for every panel. Select actual groups from this panel's coordinates first; retain all of
them when applying the shared order, including groups omitted from the ordering hint.
Use None endpoints when no interval exists. Preserve asymmetric endpoints, including an
interval that does not contain its point estimate; the helper draws those endpoints separately.
Uncomputed cells stay missing. Do not estimate uncertainty or create extra observations.

category_styles may be None. For one estimate series with meaningful category colors,
supply aligned dictionaries containing artist_label, color, edge and marker using shared identity.
Do not request an unavailable result_grid, comparison field or quantity_semantics object.

Use clear, concise scientific labels. For differences state the ordered subtraction and units,
and add a zero reference in the generated code. Label a difference-in-differences by its actual
meaning rather than pretending it is an ordinary group mean. Do not force unlike quantities
or units onto one numeric axis. Compatible results may share a panel with clearly named rows.
Common scales help compare facets of the same quantity.

If several series need a legend, create it using their actual scientific names and keep it clear
of observations. The optional label_difference_axis helper can produce full subtraction legend
labels when explicit comparison pairs are supplied; it is not required for already named rows.
The host does not add legends, zero lines, footers or raw observations.

For posterior parameter summaries, distinguish credible from confidence intervals and posterior
event probabilities. Add a threshold reference only when the supplied event and parameter unit
justify that numeric position. Do not substitute a p value for a posterior probability.

Use a short subplot identifier or empty title. The external caption defines comparisons,
interval kind/level, sample unit, missing handling and simulation status. When genuine source
observations help, draw them from data.tables, with their actual pairing and numeric values.
