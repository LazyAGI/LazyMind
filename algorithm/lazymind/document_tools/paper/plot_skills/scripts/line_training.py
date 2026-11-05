"""Observed training curves using the shared paper preset.

Pass actual observations and data-dependent limits.
"""
import numpy as np
from paper_common import paper_axes, paper_legend


def draw_training_panel(ax, series, labels, colors, edges, markers, line_styles,
                        title, xlabel, ylabel, xlim, ylim, legend_location):
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers) == len(line_styles)):
        raise ValueError('Supply matching labels and appearance for every trajectory')
    for i, label in enumerate(labels):
        x, y = (np.asarray(values, dtype=float) for values in series[label])
        if not len(x) or len(x) != len(y) or not np.isfinite(x).all() or np.isinf(y).any():
            raise ValueError('Training coordinates must align; keep missing y as NaN gaps')
        if not np.all(np.diff(x) > 0):
            raise ValueError('A trajectory needs ordered unique checkpoints; separate repeated runs first')
        ax.plot(x, y, color=colors[i], linestyle=line_styles[i], linewidth=1.25,
                marker=markers[i], markevery=max(1, len(x) // 7), markersize=3.2,
                markeredgewidth=.45, markeredgecolor=edges[i],
                markerfacecolor=colors[i], label=label, zorder=3 + i)
    paper_axes(ax, title, xlabel, ylabel, False)
    ax.set_xlim(*xlim)
    ax.set_ylim(*ylim)
    paper_legend(ax, legend_location)
