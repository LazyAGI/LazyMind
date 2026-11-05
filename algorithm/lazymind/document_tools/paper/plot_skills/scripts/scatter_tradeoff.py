"""Paired measured coordinates with shape encoding; no binning, fit or broken axis.

Uses the marker distinction of scatter_break with the approved common typography.
"""
import numpy as np
from paper_common import paper_axes, paper_legend


def draw_tradeoff(ax, series, labels, colors, edges, markers, sizes,
                  title, xlabel, ylabel, xlim, ylim, legend_location):
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers) == len(sizes)):
        raise ValueError('Supply matching labels and appearance for every series')
    for i, label in enumerate(labels):
        pairs = np.asarray(series[label], dtype=float)
        if not pairs.size:
            continue
        if pairs.ndim != 2 or pairs.shape[1] != 2 or np.isinf(pairs).any():
            raise ValueError('Scatter data must contain finite-or-missing x/y pairs')
        pairs = pairs[np.isfinite(pairs).all(axis=1)]
        if not len(pairs):
            continue
        ax.scatter(pairs[:, 0], pairs[:, 1], marker=markers[i], s=sizes[i],
                   color=colors[i], edgecolors=edges[i], linewidths=.45,
                   alpha=1, zorder=3 + i, label=label)
    paper_axes(ax, title, xlabel, ylabel, True)
    ax.set_xlim(*xlim)
    ax.set_ylim(*ylim)
    paper_legend(ax, legend_location)
