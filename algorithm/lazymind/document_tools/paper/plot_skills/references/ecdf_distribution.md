# Exact empirical cumulative distribution

Call `paper_extended.py::draw_ecdf(ax, series, methods, colors, edges, line_styles, title, xlabel, xlim)`.

Use finite raw observations, including all ties. The function groups equal x values, accumulates their exact mass and draws a post-step empirical curve from probability 0 to 1. Return values expose sorted unique observations and cumulative probabilities. No fitted distribution, smoothing or invented interpolation is added. All finite observations must fit in xlim; sample sizes and missing entries go in the caption.

Keep method identity and line styles stable. A histogram may be a complementary panel, not a mandatory duplicate. The approved double-panel layout is 182×78 mm. Preserve the same x limits if both panels compare the same measurement.
