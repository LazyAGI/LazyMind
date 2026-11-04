# Binary ROC and precision-recall

Call `paper_extended.py::draw_classification(axes, y_true, scores, methods, edges, line_styles)` with two axes. `scores[method]` must be the actual score vector for the SAME complete observed cases as binary `y_true`. Both classes must occur. Scores may be uncalibrated rankings. Never fabricate score distributions from means, accuracies or other aggregate findings.

The helper dependency `binary_curves` groups tied thresholds together, computes trapezoidal ROC AUC and noninterpolated average precision (AP). AP is not trapezoidal PR area. Reference lines are the chance ROC diagonal and the observed positive prevalence; disclose them and the held-out sample/event counts in the caption. No confidence band or significance is inferred.

Use the approved 182×78 mm paired layout. ROC and PR use unit axes and compact legends in free space, with stable darker method outlines/line styles. AUC/AP in the legend are computed from these actual vectors, not copied from metadata or a source paper.
