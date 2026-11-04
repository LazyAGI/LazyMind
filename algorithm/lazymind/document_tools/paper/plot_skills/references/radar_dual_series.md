# Two-method profiles on a common radial scale

A radar panel compares two methods across at least three named dimensions. Use
it only when radial distance has a comparable meaning on every spoke: the units,
direction and scale must agree, or an explicit scientifically justified normalization
must have been applied before drawing. Use a simpler comparison when these
conditions do not hold.

## Required call

Create polar axes with `subplot_kw={'projection': 'polar'}` and call the function
from `scripts/radar_comparison.py`:

```python
draw_radar(ax, series, labels, categories, colors, edges, markers, line_styles,
           title, radial_max, grid_levels, focal_method)
```

`labels` identifies two distinct methods. For each method, `series[label]` is a
complete one-dimensional array aligned with the same `categories`. Every value is
finite and between zero and `radial_max`. A missing dimension cannot be replaced
by zero. Use the actual category count and a consistent order between comparable
panels; do not add spokes to resemble a demonstration.

`radial_max` is a shared positive bound. `grid_levels` are explicit positive values
within that bound. They describe the value scale, while the additional radial space
created by the helper is reserved for labels. A panel-specific radial scale would
change the visual meaning of an otherwise identical value.

## Identity and arrangement

Bind appearance arrays through the global method maps. Fills use the method colors;
nonfocal outlines use their darker edges, while the explicitly supplied
`focal_method` uses its method color for the outline. Pass None when no focal method
was designated. Do not select focal status by ranking the observed values; choose
one disclosed radial scale that accommodates the compared panels.

Use the selected profile's single or paired dimensions and margins; the paired
canvas is 182 × 92 mm. The helper draws dashed scale guides, lightly filled
polygons, direct values and a legend below each polar axes. Preserve readability
at the final size. For many dimensions or long labels, select another geometry
instead of continually shrinking the type.

Keep `title` empty or a panel identifier. In metadata, state the dimensions, units,
scale, direction and any normalization reference. Polygon area is sensitive to
spoke order and is not an aggregate score or evidence of statistical superiority.
