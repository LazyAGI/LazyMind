# Ordered estimates with uncertainty bands

Select a banded curve when an estimate and its interval are available at the same
x coordinate, method and experimental setting. Checkpoints and model sizes are
possible x variables; neither determines the interval calculation. Without matching
endpoints, select an observed line instead of manufacturing a band.

## Required call

`scripts/line_selfdistill.py` provides:

```python
draw_confidence_panel(ax, series, labels, colors, edges, markers, line_styles,
                      title, xlabel, ylabel, xlim, ylim, xscale, legend_location)
```

For each displayed label, bind `series[label]=(x, mean, lower, upper)`. The four
arrays are one-dimensional, nonempty and equally long. X coordinates must be finite,
unique and strictly increasing. Endpoints are absolute values, not error lengths.
Lower and upper must be ordered and describe the same statistic as the mean.

None/NaN can mark a missing mean or interval. Both endpoints must be present or
both missing; an interval without a matching mean is invalid. A missing interval
leaves the observed line available but opens a gap in its band. Missing means remain
line gaps. Infinite values are rejected.

Construct appearance arrays by looking up each label in the global identity maps.
Use `xscale='linear'` unless a log axis serves the scientific question; log coordinates
and the lower x limit must be positive. Choose limits from all observed means and
available endpoints, and label quantities, units and any log scaling explicitly.

## Evidence and rendering

Estimate intervals before plotting from the appropriate independent units. Do not
reuse a method-wide interval at every checkpoint, derive between-run uncertainty
from checkpoints of one run, or invent a narrowing trend. If the analysis used
paired runs, resampling or another dependence structure, retain that definition.

The helper draws a 0.20-alpha band and a 1.25 pt curve with sparse markers. It does
not smooth, interpolate missing evidence or compute intervals. Its return value is
the finite mean count for each label, not the number of independent replicates.
Use the profile's single or paired layout; the paired canvas is 182 × 78 mm.

Set `title` to an empty string or panel identifier and keep the legend clear of the
curves. In metadata, explain the panels, estimate, interval method and coverage,
valid replicate counts, and whether intervals are pointwise or simultaneous.
Overlapping or separated bands alone do not establish a significance claim.
