# Density shape with all raw observations

Call `paper_extended.py::draw_violin(ax, series, methods, colors, edges, title, ylabel)`.

Requires finite nonconstant raw samples, at least five per displayed method. Different group sizes are supported. Omit None/NaN, reject infinity, and show the actual valid n once per category. Do not synthesize a violin from means/intervals alone; small or constant samples are better shown as actual points or a suitable other chart.

The Gaussian KDE uses bandwidth factor 0.30 over observed extrema, a translucent identity-colored body, every raw point exactly once, a thicker IQR line and white median point. Deterministic horizontal jitter is layout only. KDE is a density estimate, not an observation or confidence interval. No temporal smoothing is involved.

Use the approved 89×82 mm single-panel layout (left .17, right .96, bottom .23, top .87), open axes, subtle y grid, no redundant legend, and the same method colors as other figures. Caption sample units, missing values and the bandwidth.
