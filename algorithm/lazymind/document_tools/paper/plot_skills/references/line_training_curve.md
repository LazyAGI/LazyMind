# Line chart over an ordered x

Use a line chart to show how a measured value changes along an ordered numeric x:
training steps, data size, time, a parameter sweep or another ordered factor. Each
point is an observation or a verified per-x summary such as a mean; say which in the
caption. Select axes that describe the actual measurements; the name of this style
does not require training data.

## Required call

`scripts/line_training.py` provides:

```python
draw_training_panel(ax, series, labels, colors, edges, markers, line_styles,
                    title, xlabel, ylabel, xlim, ylim, legend_location)
```

`series[label]=(x, y)` binds each label to its own observed coordinates. Arrays must
be nonempty and aligned. X values are finite, unique and strictly increasing; y
values are finite or NaN. A missing y creates a visible gap. Sort paired rows by x
together rather than sorting either coordinate array independently.

Repeated checkpoints may belong to different seeds, settings or runs. Separate
them into identified trajectories, or use a verified per-checkpoint summary whose
scope is stated. Do not choose the first duplicate or silently average unlike runs.

Look up `colors`, `edges`, `markers` and `line_styles` by the current labels in the
global identity maps. Choose `xlim` and `ylim` from the displayed observations, with
enough padding to keep their markers visible. Axis text names the variables and
units. `title` is empty or a panel identifier; `legend_location` identifies a free
corner of the plot.

## Reading the result

The helper connects the supplied observations with 1.25 pt lines and sparse 3.2 pt
markers on open axes without a grid. It performs no smoothing or statistical
estimation. Preserve observed fluctuations, missing sections and method identity
across panels. Do not add reference events, extrapolated segments or uncertainty
unless they are supported by the input and the chosen interface.

Loss and validation may occupy complementary panels with different labeled value
scales. One run per method supports an observed trajectory, not a between-run
confidence band. Use `line_confidence_band` when verified endpoints exist at the
actual x coordinates.

The figure metadata explains the panel question, checkpoint definition, run counts,
any upstream aggregation and missing observations. Distinguish the number of
checkpoints from the number of independent runs.
