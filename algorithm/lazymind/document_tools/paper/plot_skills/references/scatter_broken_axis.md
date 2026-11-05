# Scatter across two separated x windows

An x-axis break is appropriate only when a scientifically relevant comparison
spans distant ranges and an empty interval can be omitted without losing observed
points. Start with ordinary scatter when a continuous axis already communicates
the data. The break must be visible and its omitted range disclosed.

## Figure ownership and call

Create an empty Figure using `style_contract['rc']['figure.figsize']`; the default
size is 182 × 76 mm. `scripts/scatter_break.py` provides:

```python
draw_broken_scatter(fig, series, labels, colors, edges, markers, sizes,
                    windows, ylim, title, xlabel, ylabel, legend_location)
```

The helper constructs and returns two axes with a shared y scale. It sets panel
widths proportional to their visible x spans, assigns margins and adds break marks
at the facing edges. Do not pre-create axes or override this arrangement with
equal-width panels or a conflicting layout engine.

## Coverage and identity

For each label, `series[label]` contains rows of `(x, y)` observations. Keep the
coordinate pairing intact. A row with a missing coordinate is omitted as a whole;
infinite coordinates are invalid. Look up colors, edges, marker shapes and sizes
from the global identity maps rather than assigning them separately in each window.

`windows=[(left_low, left_high), (right_low, right_high)]` satisfies
`left_low < left_high < right_low < right_high`. Every complete observation must
belong to exactly one window, including observations on its outer endpoints.
`ylim` includes every retained y coordinate. The helper rejects any gap or outer
limit that hides an observation; its windows do not act as data filters.

## Meaning of the display

The two panels use the same x units per physical distance and the same y scale.
Both belong to one scatter comparison. Do not draw a line across the break, imply
measurements inside the omitted interval or connect independent configurations as
a measured trajectory. Use supported analysis before adding a fitted relationship
or dominance claim.

`title` belongs in figure metadata; provide a shared `xlabel` with units and the
actual `ylabel`. Place the legend clear of points in the right panel. The caption
names the omitted x interval, explains the reason for the break and reports
incomplete coordinate pairs. If the proposed gap contains data, retain those
observations by choosing a continuous/log scale or another genuinely empty gap.
