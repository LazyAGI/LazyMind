# Trajectory overview and linked detail

Use a zoomed trajectory when a local region answers a question that is difficult
to inspect at the full observed scale. Both views must use the same measurements.
Choose the detail window from the research question and observed coordinates.

## Figure ownership and call

Start with an empty Figure sized by `style_contract['rc']['figure.figsize']`.
The default canvas is 182 × 78 mm. `scripts/line_loss_inset.py` supplies:

```python
draw_loss_zoom(fig, series, labels, colors, edges, markers, line_styles,
               titles, xlabel, ylabel, xlim, ylim, zoom_xlim, zoom_ylim,
               legend_location)
```

The helper creates and returns `(main, detail)` axes. Do not pre-create subplots,
add another pair of axes, or apply standard subplot margins afterward. Its main
axes occupy the larger left region; the right detail has a complete box and links
back to the corresponding rectangle in the overview.

## Coordinate requirements

`series[label]=(x, y)` contains nonempty, aligned one-dimensional arrays. X is finite,
unique and strictly increasing; y is finite or NaN. Keep missing y values as gaps.
Build all appearance arrays from the supplied method identity maps.

`xlim` and `ylim` must include every supplied finite observation. Both zoom ranges
have positive extent and lie inside the overview ranges. At least one finite
observation must fall inside the selected detail rectangle. The helper draws the
same arrays twice; the detail limits and overview rectangle are exactly the same
coordinate window. Do not supply a different late-stage series or rescaled y values
to the detail view.

## Interpretation and layout

Retain the detail frame and linking guides. They identify a scale change, not a
separate experiment. The helper adds sparse markers without smoothing, estimating
uncertainty or inventing training events. Any preprocessing must already be
scientifically justified and disclosed with the input evidence.

Pass two empty strings or panel identifiers in `titles`. Label the actual x variable
and loss or other response with their units. Return the figure title and panel
descriptions as metadata. The caption states the overview and detail purpose,
zoom range, preprocessing and the sampling unit; individual checkpoints are not
independent repeated experiments.
