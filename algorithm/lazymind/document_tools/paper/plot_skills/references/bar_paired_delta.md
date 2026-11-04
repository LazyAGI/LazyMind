# Two-method bars with signed differences

Use this style to show two nonnegative method values and their absolute difference
for matched categories. The order is fixed by meaning: baseline first, compared
method second. Matching categories does not by itself establish paired experimental
replicates or justify a paired statistical test.

## Required call

`scripts/bar_paired.py` provides:

```python
draw_paired_panel(ax, series, labels, categories, colors, focal_method,
                  title, xlabel, ylabel, ylim, delta_unit, legend_location)
```

`labels=[baseline_name, method_name]` contains two distinct names. For each name,
`series[name]` is aligned to the unique `categories` list. Observed values are finite
and nonnegative; None or NaN denotes a missing cell. Select both colors from their
global method identities. `focal_method` is a supplied designation or None.

The value axis is linear with `ylim=(0, upper)`. Choose `upper` from the actual data
with room for difference text. The helper rejects a range that clips observations
or leaves insufficient label space. Set figure dimensions and margins before the
call because the arrow threshold depends on the physical axes height.

## What the annotations mean

For each complete category, the helper calculates `method - baseline` in the
displayed value units. `delta_unit='pp'` is valid for percentage-point differences
only when the input values already use a 0–100 percent scale. A difference between
proportions is still in proportion units until an explicit conversion is applied.
Use the actual absolute unit for other metrics. Relative percent changes and
significance marks are outside this interface.

Bars have width 0.27 and centers at category position minus/plus 0.14. The arrow
runs from the baseline value to the method value at the shorter bar's horizontal
position, so it occupies the space above that bar. A dashed horizontal guide marks
the taller value. At output size, a difference spanning less than 3 pt retains its
signed text but receives neither arrow nor guide. Do not enlarge a small difference
by adjusting arrow endpoints. An incomplete pair keeps any observed bar and has no
difference annotation.

## Arrange the panels

Use the merged profile's dimensions and margins. The paired layout is 182 × 78 mm
and reserves a top strip for a shared legend. Comparable panels use the same value
scale. Pass `legend_location=None` for a shared figure legend, construct it from
actual bar handles, and deduplicate in global method order. For three methods the
reviewed legend uses `loc='upper center'`, `bbox_to_anchor=(0.52, 0.99)`, `ncol=3`,
`handlelength=1.7`, `columnspacing=1.6` and patch linewidth 1 pt. Adapt the available
space if the actual method count differs. A single panel may use a free in-axes
corner instead.

Keep `title` empty or a panel identifier. The caption names both methods, defines
the subtraction and its units, states whether higher or lower is preferable, and
describes aggregation and missing cells. A positive difference is not automatically
an improvement. Use another geometry for signed inputs, relative changes or
uncertainty about the difference.
