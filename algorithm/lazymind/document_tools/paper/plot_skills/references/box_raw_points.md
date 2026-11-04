# box_raw_points

Use the approved [common paper preset](paper_style.md).
Call `scripts/box_raw_points.py:draw_distribution` with arrays per group,
labels, colors, edges, short title, axis labels and data-dependent y limits.

Use for independent-run distributions. Group sizes may differ; omit nulls, never fill
with zero. Empty groups remain identified as n=0 without a box or fabricated point.

Boxes show median and IQR. Whiskers reach the extreme values within 1.5 IQR. Every valid
observation appears once as a small side-offset dot, including outliers; suppress separate
flier marks. Offsets are deterministic layout only. Show valid n once under each category,
not also in a legend. Explain box semantics, missing counts and any pairing in the caption.
A nonzero y minimum is allowed for distributions; bar zero-baseline rules do not apply.
